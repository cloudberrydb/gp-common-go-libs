package dbconn_test

import (
	"database/sql/driver"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/greenplum-db/gp-common-go-libs/dbconn"
	"github.com/greenplum-db/gp-common-go-libs/gplog"
	"github.com/greenplum-db/gp-common-go-libs/operating"
	"github.com/greenplum-db/gp-common-go-libs/testhelper"
	sqlmock "gopkg.in/DATA-DOG/go-sqlmock.v1"

	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var (
	connection *dbconn.DBConn
	mock       sqlmock.Sqlmock
	logger     *gplog.Logger
	stdout     *gbytes.Buffer
	stderr     *gbytes.Buffer
	logfile    *gbytes.Buffer
)

func ExpectBegin(mock sqlmock.Sqlmock) {
	fakeResult := testhelper.TestResult{Rows: 0}
	mock.ExpectBegin()
	mock.ExpectExec("SET TRANSACTION(.*)").WillReturnResult(fakeResult)
}

func TestDBConn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dbconn tests")
}

var _ = BeforeSuite(func() {
	connection, mock, logger, stdout, stderr, logfile = testhelper.SetupTestEnvironment()
	dbconn.SetLogger(logger)
})

var _ = BeforeEach(func() {
	connection, mock = testhelper.CreateAndConnectMockDB(1)
})

var _ = Describe("dbconn/dbconn tests", func() {
	BeforeEach(func() {
		operating.System.Now = func() time.Time { return time.Date(2017, time.January, 1, 1, 1, 1, 1, time.Local) }
	})
	Describe("NewDBConn", func() {
		It("gets the DBName from the dbname flag if it is set", func() {
			connection = dbconn.NewDBConn("testdb")
			Expect(connection.DBName).To(Equal("testdb"))
		})
		It("fails if no database is given with the dbname flag", func() {
			defer testhelper.ShouldPanicWithMessage("No database provided")
			connection = dbconn.NewDBConn("")
		})
	})
	Describe("DBConn.MustConnect", func() {
		var mockdb *sqlx.DB
		BeforeEach(func() {
			mockdb, mock = testhelper.CreateMockDB()
			if connection != nil {
				connection.Close()
			}
			connection = dbconn.NewDBConn("testdb")
			connection.Driver = testhelper.TestDriver{DB: mockdb, User: "testrole"}
		})
		AfterEach(func() {
			if connection != nil {
				connection.Close()
			}
		})
		It("makes a single connection successfully if the database exists", func() {
			connection.MustConnect(1)
			Expect(connection.DBName).To(Equal("testdb"))
			Expect(connection.NumConns).To(Equal(1))
			Expect(len(connection.ConnPool)).To(Equal(1))
		})
		It("makes multiple connections successfully if the database exists", func() {
			connection.MustConnect(3)
			Expect(connection.DBName).To(Equal("testdb"))
			Expect(connection.NumConns).To(Equal(3))
			Expect(len(connection.ConnPool)).To(Equal(3))
		})
		It("does not connect if the database exists but the connection is refused", func() {
			connection.Driver = testhelper.TestDriver{ErrToReturn: fmt.Errorf("pq: connection refused"), DB: mockdb, User: "testrole"}
			defer testhelper.ShouldPanicWithMessage(`could not connect to server: Connection refused`)
			connection.MustConnect(1)
		})
		It("fails if an invalid number of connections is given", func() {
			defer testhelper.ShouldPanicWithMessage("Must specify a connection pool size that is a positive integer")
			connection.MustConnect(0)
		})
		It("fails if the database does not exist", func() {
			connection.Driver = testhelper.TestDriver{ErrToReturn: fmt.Errorf("pq: database \"testdb\" does not exist"), DB: mockdb, DBName: "testdb", User: "testrole"}
			Expect(connection.DBName).To(Equal("testdb"))
			defer testhelper.ShouldPanicWithMessage("Database \"testdb\" does not exist, exiting")
			connection.MustConnect(1)
		})
		It("fails if the role does not exist", func() {
			oldPgUser := os.Getenv("PGUSER")
			os.Setenv("PGUSER", "nonexistent")
			defer os.Setenv("PGUSER", oldPgUser)

			connection = dbconn.NewDBConn("testdb")
			connection.Driver = testhelper.TestDriver{ErrToReturn: fmt.Errorf("pq: role \"nonexistent\" does not exist"), DB: mockdb, DBName: "testdb", User: "nonexistent"}
			Expect(connection.User).To(Equal("nonexistent"))
			defer testhelper.ShouldPanicWithMessage("Role \"nonexistent\" does not exist, exiting")
			connection.MustConnect(1)
		})
	})
	Describe("DBConn.Close", func() {
		var mockdb *sqlx.DB
		BeforeEach(func() {
			mockdb, mock = testhelper.CreateMockDB()
			connection = dbconn.NewDBConn("testdb")
			connection.Driver = testhelper.TestDriver{DB: mockdb, User: "testrole"}
		})
		It("successfully closes a dbconn with a single open connection", func() {
			connection.MustConnect(1)
			Expect(connection.NumConns).To(Equal(1))
			Expect(len(connection.ConnPool)).To(Equal(1))
			connection.Close()
			Expect(connection.NumConns).To(Equal(0))
			Expect(connection.ConnPool).To(BeNil())
		})
		It("successfully closes a dbconn with multiple open connections", func() {
			connection.MustConnect(3)
			Expect(connection.NumConns).To(Equal(3))
			Expect(len(connection.ConnPool)).To(Equal(3))
			connection.Close()
			Expect(connection.NumConns).To(Equal(0))
			Expect(connection.ConnPool).To(BeNil())
		})
		It("does nothing if there are no open connections", func() {
			connection.MustConnect(3)
			connection.Close()
			Expect(connection.NumConns).To(Equal(0))
			Expect(connection.ConnPool).To(BeNil())
			connection.Close()
			Expect(connection.NumConns).To(Equal(0))
			Expect(connection.ConnPool).To(BeNil())
		})
	})
	Describe("DBConn.Exec", func() {
		It("executes an INSERT outside of a transaction", func() {
			fakeResult := testhelper.TestResult{Rows: 1}
			mock.ExpectExec("INSERT (.*)").WillReturnResult(fakeResult)

			res, err := connection.Exec("INSERT INTO pg_tables VALUES ('schema', 'table')")
			Expect(err).ToNot(HaveOccurred())
			rowsReturned, err := res.RowsAffected()
			Expect(rowsReturned).To(Equal(int64(1)))
		})
		It("executes an INSERT in a transaction", func() {
			fakeResult := testhelper.TestResult{Rows: 1}
			ExpectBegin(mock)
			mock.ExpectExec("INSERT (.*)").WillReturnResult(fakeResult)
			mock.ExpectCommit()

			connection.MustBegin()
			res, err := connection.Exec("INSERT INTO pg_tables VALUES ('schema', 'table')")
			connection.MustCommit()
			Expect(err).ToNot(HaveOccurred())
			rowsReturned, err := res.RowsAffected()
			Expect(rowsReturned).To(Equal(int64(1)))
		})
	})
	Describe("DBConn.Get", func() {
		It("executes a GET outside of a transaction", func() {
			two_col_single_row := sqlmock.NewRows([]string{"schemaname", "tablename"}).
				AddRow("schema1", "table1")
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(two_col_single_row)

			testRecord := struct {
				Schemaname string
				Tablename  string
			}{}

			err := connection.Get(&testRecord, "SELECT schemaname, tablename FROM two_columns ORDER BY schemaname")

			Expect(err).ToNot(HaveOccurred())
			Expect(testRecord.Schemaname).To(Equal("schema1"))
			Expect(testRecord.Tablename).To(Equal("table1"))
		})
		It("executes a GET in a transaction", func() {
			two_col_single_row := sqlmock.NewRows([]string{"schemaname", "tablename"}).
				AddRow("schema1", "table1")
			ExpectBegin(mock)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(two_col_single_row)
			mock.ExpectCommit()

			testRecord := struct {
				Schemaname string
				Tablename  string
			}{}

			connection.MustBegin()
			err := connection.Get(&testRecord, "SELECT schemaname, tablename FROM two_columns ORDER BY schemaname")
			connection.MustCommit()
			Expect(err).ToNot(HaveOccurred())
			Expect(testRecord.Schemaname).To(Equal("schema1"))
			Expect(testRecord.Tablename).To(Equal("table1"))
		})
	})
	Describe("DBConn.Select", func() {
		It("executes a SELECT outside of a transaction", func() {
			two_col_rows := sqlmock.NewRows([]string{"schemaname", "tablename"}).
				AddRow("schema1", "table1").
				AddRow("schema2", "table2")
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(two_col_rows)

			testSlice := make([]struct {
				Schemaname string
				Tablename  string
			}, 0)

			err := connection.Select(&testSlice, "SELECT schemaname, tablename FROM two_columns ORDER BY schemaname LIMIT 2")

			Expect(err).ToNot(HaveOccurred())
			Expect(len(testSlice)).To(Equal(2))
			Expect(testSlice[0].Schemaname).To(Equal("schema1"))
			Expect(testSlice[0].Tablename).To(Equal("table1"))
			Expect(testSlice[1].Schemaname).To(Equal("schema2"))
			Expect(testSlice[1].Tablename).To(Equal("table2"))
		})
		It("executes a SELECT in a transaction", func() {
			two_col_rows := sqlmock.NewRows([]string{"schemaname", "tablename"}).
				AddRow("schema1", "table1").
				AddRow("schema2", "table2")
			ExpectBegin(mock)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(two_col_rows)
			mock.ExpectCommit()

			testSlice := make([]struct {
				Schemaname string
				Tablename  string
			}, 0)

			connection.MustBegin()
			err := connection.Select(&testSlice, "SELECT schemaname, tablename FROM two_columns ORDER BY schemaname LIMIT 2")
			connection.MustCommit()

			Expect(err).ToNot(HaveOccurred())
			Expect(len(testSlice)).To(Equal(2))
			Expect(testSlice[0].Schemaname).To(Equal("schema1"))
			Expect(testSlice[0].Tablename).To(Equal("table1"))
			Expect(testSlice[1].Schemaname).To(Equal("schema2"))
			Expect(testSlice[1].Tablename).To(Equal("table2"))
		})
	})
	Describe("DBConn.MustBegin", func() {
		It("successfully executes a BEGIN outside a transaction", func() {
			ExpectBegin(mock)
			connection.MustBegin()
			Expect(connection.Tx).To(Not(BeNil()))
		})
		It("panics if it executes a BEGIN in a transaction", func() {
			ExpectBegin(mock)
			connection.MustBegin()
			defer testhelper.ShouldPanicWithMessage("Cannot begin transaction; there is already a transaction in progress")
			connection.MustBegin()
		})
	})
	Describe("DBConn.MustCommit", func() {
		It("successfully executes a COMMIT in a transaction", func() {
			ExpectBegin(mock)
			mock.ExpectCommit()
			connection.MustBegin()
			connection.MustCommit()
			Expect(connection.Tx).To(BeNil())
		})
		It("panics if it executes a COMMIT outside a transaction", func() {
			defer testhelper.ShouldPanicWithMessage("Cannot commit transaction; there is no transaction in progress")
			connection.MustCommit()
		})
	})
	Describe("Dbconn.ValidateConnNum", func() {
		BeforeEach(func() {
			connection.Close()
			connection.MustConnect(3)
		})
		AfterEach(func() {
			connection.Close()
		})
		It("returns the connection number if it is valid", func() {
			num := connection.ValidateConnNum(1)
			Expect(num).To(Equal(1))
		})
		It("defaults to 0 with no argument", func() {
			num := connection.ValidateConnNum()
			Expect(num).To(Equal(0))
		})
		It("panics if given multiple arguments", func() {
			defer testhelper.ShouldPanicWithMessage("At most one connection number may be specified for a given connection")
			connection.ValidateConnNum(1, 2)
		})
		It("panics if given a negative number", func() {
			defer testhelper.ShouldPanicWithMessage("Invalid connection number: -1")
			connection.ValidateConnNum(-1)
		})
		It("panics if given a number greater than NumConns", func() {
			defer testhelper.ShouldPanicWithMessage("Invalid connection number: 4")
			connection.ValidateConnNum(4)
		})
	})
	Describe("MustSelectString", func() {
		header := []string{"string"}
		rowOne := []driver.Value{"one"}
		rowTwo := []driver.Value{"two"}

		It("returns a single string if the query selects a single string", func() {
			fakeResult := sqlmock.NewRows(header).AddRow(rowOne...)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			result := dbconn.MustSelectString(connection, "SELECT foo FROM bar")
			Expect(result).To(Equal("one"))
		})
		It("returns an empty string if the query selects no strings", func() {
			fakeResult := sqlmock.NewRows(header)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			result := dbconn.MustSelectString(connection, "SELECT foo FROM bar")
			Expect(result).To(Equal(""))
		})
		It("panics if the query selects multiple strings", func() {
			fakeResult := sqlmock.NewRows(header).AddRow(rowOne...).AddRow(rowTwo...)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			defer testhelper.ShouldPanicWithMessage("Too many rows returned from query: got 2 rows, expected 1 row")
			dbconn.MustSelectString(connection, "SELECT foo FROM bar")
		})
	})
	Describe("MustSelectStringSlice", func() {
		header := []string{"string"}
		rowOne := []driver.Value{"one"}
		rowTwo := []driver.Value{"two"}

		It("returns a slice containing a single string if the query selects a single string", func() {
			fakeResult := sqlmock.NewRows(header).AddRow(rowOne...)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			results := dbconn.MustSelectStringSlice(connection, "SELECT foo FROM bar")
			Expect(len(results)).To(Equal(1))
			Expect(results[0]).To(Equal("one"))
		})
		It("returns an empty slice if the query selects no strings", func() {
			fakeResult := sqlmock.NewRows(header)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			results := dbconn.MustSelectStringSlice(connection, "SELECT foo FROM bar")
			Expect(len(results)).To(Equal(0))
		})
		It("returns a slice containing multiple strings if the query selects multiple strings", func() {
			fakeResult := sqlmock.NewRows(header).AddRow(rowOne...).AddRow(rowTwo...)
			mock.ExpectQuery("SELECT (.*)").WillReturnRows(fakeResult)
			results := dbconn.MustSelectStringSlice(connection, "SELECT foo FROM bar")
			Expect(len(results)).To(Equal(2))
			Expect(results[0]).To(Equal("one"))
			Expect(results[1]).To(Equal("two"))
		})
	})
})