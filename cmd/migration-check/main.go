package main

import (
	"flag"
	"log"

	"github.com/SnWalker/kowa/internal/platform/migrations"
)

func main() {
	directory := flag.String("dir", "db/migrations", "directory containing paired SQL migrations")
	flag.Parse()

	if err := migrations.Check(*directory); err != nil {
		log.Fatal(err)
	}
}
