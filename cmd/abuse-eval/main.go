package main

import (
	"encoding/json"
	"log"
	"miniplay/internal/abuse"
	"os"
)

func main() {
	metrics, err := abuse.EvaluateInjected(47211)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(metrics); err != nil {
		log.Fatal(err)
	}
}
