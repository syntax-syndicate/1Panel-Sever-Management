package migration

import (
	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/1Panel-dev/1Panel/agent/init/migration/migrations"

	"github.com/go-gormigrate/gormigrate/v2"
)

func Init() {
	InitAgentDB()
	InitTaskDB()
	global.LOG.Info("Migration run successfully")
}

func InitAgentDB() {
	m := gormigrate.New(global.DB, gormigrate.DefaultOptions, []*gormigrate.Migration{
		migrations.AddTable,
		migrations.AddMonitorTable,
		migrations.InitSetting,
		migrations.InitImageRepo,
		migrations.InitDefaultCA,
		migrations.InitPHPExtensions,
	})
	if err := m.Migrate(); err != nil {
		global.LOG.Error(err)
		panic(err)
	}
}

func InitTaskDB() {
	m := gormigrate.New(global.TaskDB, gormigrate.DefaultOptions, []*gormigrate.Migration{
		migrations.AddTaskTable,
	})
	if err := m.Migrate(); err != nil {
		global.LOG.Error(err)
		panic(err)
	}
}
