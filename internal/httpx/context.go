package httpx

import (
	"context"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

type key string
const identityKey key = "identity"
func WithIdentity(ctx context.Context,id model.Identity) context.Context{return context.WithValue(ctx,identityKey,id)}
func IdentityFrom(ctx context.Context)(model.Identity,bool){v,ok:=ctx.Value(identityKey).(model.Identity); return v,ok}
