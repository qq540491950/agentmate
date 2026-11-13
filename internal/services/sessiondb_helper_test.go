package services

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"
)

// openSessionServiceImpl 镜像 main.go 的 newSessionService（后者位于 package
// main 中且无法被导入），因此自愈路径可测试。
func openSessionServiceImpl(t *testing.T, path string) (*gorm.DB, session.Service, error) {
	t.Helper()
	open := func() (*gorm.DB, session.Service, error) {
		db, err := gorm.Open(sqlite.Open(path))
		if err != nil {
			return nil, nil, err
		}
		svc, err := database.NewSessionServiceFromDB(db)
		if err != nil {
			return nil, nil, err
		}
		if err := database.AutoMigrate(svc); err != nil {
			return nil, nil, err
		}
		// 探测：损坏的文件可能通过 Open，但在首次查询时失败。
		if err := db.Exec("SELECT count(*) FROM sessions").Error; err != nil {
			return nil, nil, err
		}
		return db, svc, nil
	}
	db, svc, err := open()
	if err != nil {
		backup := path + ".corrupt-test"
		_ = os.Rename(path, backup)
		db, svc, err = open()
		if err != nil {
			return nil, nil, err
		}
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db, svc, nil
}
