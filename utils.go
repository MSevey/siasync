package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// checksumFile returns a sha256 checksum or size of a given file on disk depending on a options provided
func checksumFile(path string) (string, error) {
	checksum, err := sizeFile(path)
	if err != nil {
		return "", err
	}
	return checksum, nil
}

// contains checks if a string exists in a []strings.
func contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

// return SiaPath for relative file name with prefix appended
func getSiaPath(relpath string) modules.SiaPath {
	return newSiaPath(filepath.Join(siaStagingDir, relpath))
}

func newSiaPath(path string) (siaPath modules.SiaPath) {
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		panic(err)
	}
	return siaPath
}

// returns file size
func sizeFile(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	size := stat.Size()

	return strconv.FormatInt(size, 10), err
}

func uploadRetry(sf *SiaFolder, filename string) {
	err := sf.handleCreate(filename)
	if err != nil {
		// check if we have received create event for a file that is already in sia
		exists, err := sf.isFile(filename)
		if err != nil {
			log.Println(err)
		}
		if exists && !archive {
			err := sf.handleRemove(filename)
			if err != nil {
				log.Println(err)
			}
		}

		err2 := sf.handleCreate(filename)
		if err2 != nil {
			log.Println(err2)
		}
	}
}
