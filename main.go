package main

import (
	"pipevid/view-sync/database"
	"pipevid/view-sync/module"
	"pipevid/view-sync/util"

	"log"
	"os"

	"github.com/bamzi/jobrunner"
	"go.uber.org/zap"
)

func main() {
	/* Init Logger */
	logger, err := util.InitLogger()
	if err != nil {
		log.Printf("Worker: create logger error: %s\n", err.Error())
		os.Exit(1)
	}

	/* Init redis */
	rd, err := database.ConnectRedis()
	if err != nil {
		logger.Fatal("Worker: connect redis error", zap.Error(err))
		os.Exit(4)
	}

	viewSync := &module.ViewSync{Redis: rd, Logger: logger}

	/* One-shot mode (tests, manual flush): sync once and exit */
	if os.Getenv("VIEW_SYNC_ONCE") == "true" {
		viewSync.Run()
		return
	}

	/* Init worker */
	jobrunner.Start()

	/* View Sync - Sync Redis view counters to PostgreSQL (default every 2 minutes) */
	interval := os.Getenv("VIEW_SYNC_INTERVAL")
	if interval == "" {
		interval = "2m"
	}
	if err := jobrunner.Schedule("@every "+interval, viewSync); err != nil {
		logger.Fatal("Worker: invalid VIEW_SYNC_INTERVAL", zap.String("interval", interval), zap.Error(err))
	}
	logger.Info("Worker: ViewSync job scheduled", zap.String("every", interval))

	select {}
}
