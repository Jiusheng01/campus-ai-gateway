package httpx

import (
	"encoding/json"
	"net/http"
)
func Error(w http.ResponseWriter,status int,code,message,requestID string){w.Header().Set("Content-Type","application/json"); w.WriteHeader(status); _=json.NewEncoder(w).Encode(map[string]any{"error":map[string]any{"code":code,"message":message,"request_id":requestID}})}
