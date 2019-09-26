package main

// This is a trimmed down version of siasync for the sia stream MVP
//
// Operations
//
// -Watch a local folder for changes
// -If there are changes (ie new files) upload those files to /fuse/staging
// -Watch the uploaded files, once available move to production folder
//
// Assumptions
// - Eddie passes in API Password
// - Eddie passes in sia staging folder
// - Eddie passes in sia prod folder
// - Eddie passes in local folder

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	sia "gitlab.com/NebulousLabs/Sia/node/api/client"
)

const (
	archive      = true
	dataPieces   = 10
	parityPieces = 30

	movieDir = "movies"
	tvDir    = "tv"
)

var (
	password      string
	siaStagingDir string
	siaProdDir    string
	dryRun        bool
)

// Usage displays an example of how siasync should be used as well as the
// default
func Usage() {
	fmt.Printf(`usage: siasync <flags> <directory-to-sync>
  for example: ./siasync -password abcd123 /tmp/sync/to/sia

`)
	flag.PrintDefaults()
}

func main() {
	flag.Usage = Usage
	address := flag.String("address", "127.0.0.1:9980", "Sia's API address")
	flag.StringVar(&password, "password", "", "Sia's API password")
	agent := flag.String("agent", "Sia-Agent", "Sia agent")
	flag.StringVar(&siaStagingDir, "siaStagingDir", "fuse/staging", "Folder on Sia to sync files too for staging")  //  we could hard code this
	flag.StringVar(&siaProdDir, "siaProdDir", "fuse/prod", "Folder on Sia files should be moved to for production") //  we could hard code this
	flag.BoolVar(&dryRun, "dry-run", false, "Show what would have been uploaded without changing files in Sia")

	flag.Parse()

	sc := sia.New(*address)
	sc.UserAgent = *agent
	directory := os.Args[len(os.Args)-1]

	sf, err := NewSiafolder(directory, sc)
	if err != nil {
		log.Fatal(err)
	}
	defer sf.Close()

	log.Println("watching for changes to ", directory)

	done := make(chan os.Signal)
	signal.Notify(done, os.Interrupt)
	<-done
	fmt.Println("caught quit signal, exiting...")
	log.Println("Done")
}
