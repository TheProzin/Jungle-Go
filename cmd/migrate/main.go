package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/TheProzin/Jungle-Go/internal/infra/migrate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	switch os.Args[1] {
	case "up":
		if err := migrate.Up(databaseURL); err != nil {
			log.Fatal(err)
		}
		fmt.Println("migrations applied")
	case "down":
		steps := 1
		if len(os.Args) > 2 {
			parsed, err := strconv.Atoi(os.Args[2])
			if err != nil || parsed < 1 {
				log.Fatalf("invalid down steps: %q", os.Args[2])
			}
			steps = parsed
		}
		if err := migrate.Down(databaseURL, steps); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("reverted %d migration(s)\n", steps)
	case "version":
		version, dirty, err := migrate.Version(databaseURL)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: migrate <up|down [steps]|version>")
	os.Exit(1)
}
