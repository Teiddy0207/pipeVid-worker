package database

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func ConnectRedis() (*redis.Client, error) {
	var timeOut time.Duration
	if value, err := strconv.ParseInt(os.Getenv("DB_TIMEOUT"), 10, 64); err != nil {
		timeOut = 30 * time.Second
	} else {
		timeOut = time.Duration(value) * time.Second
	}
	var ctx, cancel = context.WithTimeout(context.Background(), timeOut)
	defer cancel()

	/* If connect is Sentinel */
	if os.Getenv("REDIS_MODE") == "sentinel" {
		var rd = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       os.Getenv("REDIS_MASTER_NAME"),
			SentinelAddrs:    strings.Split(os.Getenv("REDIS_SENTINEL_SERVERS"), ";"),
			SentinelPassword: os.Getenv("REDIS_SENTINEL_PASSWORD"),
			Password:         os.Getenv("REDIS_PASSWORD"),
		})

		if _, err := rd.Ping(ctx).Result(); err != nil {
			return nil, err
		} else {
			return rd, nil
		}
	}

	/* Default */
	var rd = redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_ADDR"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})

	if _, err := rd.Ping(ctx).Result(); err != nil {
		return nil, err
	} else {
		return rd, nil
	}
}
