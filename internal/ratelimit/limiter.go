package ratelimit

import (
	"context"
	"fmt"
	"time"
	"github.com/redis/go-redis/v9"
)

var incr = redis.NewScript("local c=redis.call('INCRBY',KEYS[1],ARGV[1]); if c==tonumber(ARGV[1]) then redis.call('EXPIRE',KEYS[1],ARGV[2]) end; return c")

type Limiter struct{ rdb *redis.Client }
func New(rdb *redis.Client)*Limiter{return &Limiter{rdb:rdb}}
func bucket() string{return time.Now().UTC().Format("200601021504")}
func (l *Limiter) AllowRPM(ctx context.Context,keyID string,limit int64)(bool,error){if limit<=0{return true,nil}; key:=fmt.Sprintf("gw:rl:key:%s:rpm:%s",keyID,bucket()); n,err:=incr.Run(ctx,l.rdb,[]string{key},1,120).Int64(); return n<=limit,err}
func (l *Limiter) ReserveTPM(ctx context.Context,keyID string,amount,limit int64)(bool,error){if limit<=0{return true,nil}; key:=fmt.Sprintf("gw:rl:key:%s:tpm:%s",keyID,bucket()); n,err:=incr.Run(ctx,l.rdb,[]string{key},amount,120).Int64(); if err!=nil{return false,err}; if n>limit{_ = l.rdb.DecrBy(ctx,key,amount).Err(); return false,nil}; return true,nil}
func (l *Limiter) AcquireConcurrency(ctx context.Context,keyID string,limit int64)(func(),bool,error){if limit<=0{return func(){},true,nil}; key:="gw:concurrency:key:"+keyID; n,err:=l.rdb.Incr(ctx,key).Result(); if err!=nil{return nil,false,err}; _=l.rdb.Expire(ctx,key,15*time.Minute).Err(); if n>limit{_=l.rdb.Decr(ctx,key).Err(); return nil,false,nil}; return func(){_=l.rdb.Decr(context.Background(),key).Err()},true,nil}
