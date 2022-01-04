package main

import (
	"context"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

type Store interface {
	GetDel(context.Context, string) (string, error)
	SetEx(context.Context, string, string, time.Duration) error
}

type value struct {
	exp time.Time
	val string
}

type memStore struct {
	m sync.Mutex
	d map[string]value
}

func NewMemStore() Store {
	return &memStore{d: make(map[string]value)}
}

func (ms *memStore) GetDel(_ context.Context, key string) (string, error) {
	ms.m.Lock()
	defer ms.m.Unlock()

	val, ok := ms.d[key]
	if !ok {
		return "", redis.Nil
	}
	if time.Now().After(val.exp) {
		delete(ms.d, key)

		return "", redis.Nil
	}

	delete(ms.d, key)

	return val.val, nil
}

func (ms *memStore) SetEx(_ context.Context, key, val string, exp time.Duration) error {
	ms.m.Lock()
	defer ms.m.Unlock()

	ms.d[key] = value{val: val, exp: time.Now().Add(exp)}

	return nil
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
