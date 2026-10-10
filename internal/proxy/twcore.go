package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

func NewThinkWatchProxy(targetURL,gatewayKey string)(*httputil.ReverseProxy,error){
	target,err:=url.Parse(targetURL); if err!=nil{return nil,err}
	p:=httputil.NewSingleHostReverseProxy(target)
	p.FlushInterval=-1
	orig:=p.Director
	p.Director=func(req *http.Request){orig(req); req.Host=target.Host; if gatewayKey!=""{req.Header.Set("Authorization","Bearer "+gatewayKey)}}
	p.ModifyResponse=func(resp *http.Response) error{resp.Header.Set("X-Accel-Buffering","no"); return nil}
	return p,nil
}
