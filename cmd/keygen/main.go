package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

func main() {
	secret := os.Getenv("API_KEY_HMAC_SECRET")
	if secret == "" { log.Fatal("API_KEY_HMAC_SECRET is required") }
	plain,prefix,digest,err:=security.NewAPIKey(secret)
	if err!=nil{log.Fatal(err)}
	fmt.Println("api_key="+plain)
	fmt.Println("key_prefix="+prefix)
	fmt.Println("key_digest="+digest)
}
