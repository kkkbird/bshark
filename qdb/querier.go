package qdb

//copy from https://github.com/golang-sql/sqlexp/blob/master/querier.go
import (
	"context"
	"database/sql"
)

// Querier is the common interface to execute queries on a DB, Tx, or Conn.
type Querier interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

var (
	_ Querier = &sql.DB{}
	_ Querier = &sql.Tx{}
)
