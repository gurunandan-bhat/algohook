package model

import (
	"algohook/internal/config"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

type Model struct {
	DbHandle *sqlx.DB
}

func NewModel(cfg *config.Config) (*Model, error) {

	dbCfg := mysql.NewConfig()

	dbCfg.User = cfg.Db.User
	dbCfg.Passwd = cfg.Db.Passwd
	dbCfg.Net = cfg.Db.Net
	dbCfg.Addr = cfg.Db.Addr
	dbCfg.DBName = cfg.Db.DBName
	dbCfg.ParseTime = cfg.Db.ParseTime

	tz, err := time.LoadLocation(cfg.Db.Loc)
	if err != nil {
		log.Fatalf("Error fetching local timezone: %s", err)
	}
	dbCfg.Loc = tz

	dbCfg.AllowNativePasswords = cfg.Db.AllowNativePasswords

	dbHandle, err := sqlx.Connect("mysql", dbCfg.FormatDSN())
	if err != nil {
		return nil, err
	}

	if err := dbHandle.Ping(); err != nil {
		return nil, err
	}
	(dbHandle.DB).SetConnMaxIdleTime(1 * time.Hour)

	return &Model{
		DbHandle: dbHandle,
	}, nil
}
