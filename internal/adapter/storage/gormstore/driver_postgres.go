//go:build postgres || !sqlite

package gormstore

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func init() {
	register("postgres", driver{
		dialector: func(dsn string) (gorm.Dialector, error) {
			return postgres.Open(dsn), nil
		},
		sqlDialect: "postgres",
	})
}
