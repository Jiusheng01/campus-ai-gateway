package storage

import (
	"context"
	"github.com/redis/go-redis/v9"
)

type Redis struct{ Client *redis.Client }
func OpenRedis(ctx context.Context,addr,password string,db int)(*Redis,error){
	c:=redis.NewClient(&redis.Options{Addr:addr,Password:password,DB:db})
	if err:=c.Ping(ctx).Err(); err!=nil{return nil,err}
	return &Redis{Client:c},nil
}
func (r *Redis) Close() error{return r.Client.Close()}
