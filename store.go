package main

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

type Store interface {
	GetDel(context.Context, string) (string, error)
	SetEx(context.Context, string, string, time.Duration) error
}

type redisStore struct {
	r *redis.Client
}

func NewRedisStore(r *redis.Client) Store {
	return &redisStore{r: r}
}

func (rs *redisStore) GetDel(ctx context.Context, key string) (string, error) {
	return rs.r.GetDel(ctx, key).Result()
}

func (rs *redisStore) SetEx(ctx context.Context, key, val string, exp time.Duration) error {
	return rs.r.SetEX(ctx, key, val, exp).Err()
}
