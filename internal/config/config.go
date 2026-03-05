package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultConfigFileName = ".algohook.json"
)

type Config struct {
	InProduction bool           `mapstructure:"inProduction"`
	AppRoot      string         `mapstructure:"appRoot"`
	AppHost      string         `mapstructure:"appHost"`
	AppPort      int            `mapstructure:"appPort"`
	TimeZone     *time.Location `mapstructure:"-"`
	Db           struct {
		User                 string `mapstructure:"user"`
		Passwd               string `mapstructure:"passwd"`
		Net                  string `mapstructure:"net"`
		Addr                 string `mapstructure:"addr"`
		DBName               string `mapstructure:"dbName"`
		ParseTime            bool   `mapstructure:"parseTime"`
		Loc                  string `mapstructure:"loc"`
		AllowNativePasswords bool   `mapstructure:"allowNativePasswords"`
	} `mapstructure:"db"`
	API struct {
		Key string `mapstructure:"key"`
	} `mapstructure:"api"`
	SMTP struct {
		Server    string `mapstructure:"server"`
		Port      int    `mapstructure:"port"`
		User      string `mapstructure:"user"`
		Password  string `mapstructure:"password"`
		TestEmail string `mapstructure:"test_email"`
	} `mapstructure:"smtp"`
}

var c = Config{}

func Configuration(configFileName ...string) (*Config, error) {

	if (c == Config{}) {

		var cfName string
		switch len(configFileName) {
		case 0:
			dirname, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			cfName = fmt.Sprintf("%s/%s", dirname, defaultConfigFileName)
		case 1:
			cfName = configFileName[0]
		default:
			return nil, fmt.Errorf("incorrect arguments for configuration file name")
		}

		viper.SetConfigFile(cfName)
		if err := viper.ReadInConfig(); err != nil {
			return nil, err
		}

		if err := viper.Unmarshal(&c); err != nil {
			return nil, err
		}

		var err error
		c.TimeZone, err = time.LoadLocation(c.Db.Loc)
		if err != nil {
			return nil, fmt.Errorf("error loading Time Zone: err")
		}
	}

	return &c, nil
}
