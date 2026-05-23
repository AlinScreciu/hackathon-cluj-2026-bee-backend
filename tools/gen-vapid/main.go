package main

import (
	"fmt"
	"log"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatalf("generate VAPID keys: %v", err)
	}
	fmt.Printf("VAPID_PRIVATE_KEY=%s\nVAPID_PUBLIC_KEY=%s\n", priv, pub)
}
