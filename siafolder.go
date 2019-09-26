package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	sia "gitlab.com/NebulousLabs/Sia/node/api/client"
)

// SiaFolder is a folder that is synchronized to a Sia node.
type SiaFolder struct {
	path          string
	client        *sia.Client
	archive       bool
	siaStagingDir string
	siaProdDir    string
	watcher       *fsnotify.Watcher

	files map[string]string // files is a map of file paths to SHA256 checksums, used to reconcile file changes

	dirs map[string]bool // dirs is a map of dir path to whether or not they are on sia

	closeChan chan struct{}
}

// NewSiafolder creates a new SiaFolder using the provided path and api
// address.
func NewSiafolder(path string, client *sia.Client) (*SiaFolder, error) {
	sf := &SiaFolder{}

	abspath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	sf.path = abspath
	sf.files = make(map[string]string)
	sf.dirs = make(map[string]bool)
	sf.closeChan = make(chan struct{})
	sf.client = client
	sf.archive = archive
	sf.siaStagingDir = siaStagingDir
	sf.siaProdDir = siaProdDir

	// watch for file changes
	sf.watcher = nil
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = watcher.Add(abspath)
	if err != nil {
		return nil, err
	}

	sf.watcher = watcher

	// walk the provided path, accumulating a slice of files to potentially
	// upload and adding any subdirectories to the watcher.
	err = filepath.Walk(abspath, func(walkpath string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if walkpath == path {
			return nil
		}

		// subdirectories must be added to the watcher. And added to the dirs
		// map
		if f.IsDir() {
			// This is all the movies and tv dirs to the watcher
			log.Println("Found directory", walkpath, "added to watcher and added to dirs map")
			sf.watcher.Add(walkpath)
			sf.dirs[walkpath] = false
			return nil
		}

		// Calculate check sum of files and add to files map
		log.Println("Calculating checksum for:", walkpath)
		checksum, err := checksumFile(walkpath)
		if err != nil {
			return err
		}
		log.Println("Adding file to files map:", walkpath)
		sf.files[walkpath] = checksum
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Upload any non existing
	log.Println("Uploading files missing from Sia")
	err = sf.uploadNonExisting()
	if err != nil {
		return nil, err
	}

	// Upload any files that were changed since the last run, this is done based
	// on size of file alone
	log.Println("Uploading changed files")
	err = sf.uploadChanged()
	if err != nil {
		return nil, err
	}

	// run the event watcher in a go routine
	go sf.eventWatcher()

	// Spawn a process for watching the upload status of files on Sia and move
	// them from staging to production
	go sf.moveToProductionLoop()

	return sf, nil
}

// moveToProductionLoop is an infinite loop that checks the redundancy of the
// directories in the staging directory and moves them to the production
// directory once they are healthy enough
func (sf *SiaFolder) moveToProductionLoop() {
	for {
		select {
		case <-sf.closeChan:
			return
		case <-time.After(5 * time.Second):
			// Check movies directories in staging
			stagingMoviesDir, err := sf.getStagingSiaDir(movieDir)
			if err != nil {
				log.Println("Error getting staging directory:", err)
				continue
			}

			// Check if available
			for i, dir := range stagingMoviesDir.Directories {
				// The first directory is always the requested directory, skip
				// it
				if i == 0 {
					continue
				}

				// Check if sub directory as reach at least 1x redundancy
				if dir.AggregateMinRedundancy <= 1 {
					continue
				}

				// Move Directory to production
				log.Println("move", dir.SiaPath.String(), "to production")
				err = sf.moveToProduction(dir.SiaPath)
				if err != nil {
					log.Println("error moving directory to production:", err)
				}
			}

			// Check tv directories in staging
			stagingTVDir, err := sf.getStagingSiaDir(tvDir)
			if err != nil {
				log.Println("Error getting staging directory:", err)
				continue
			}

			// Check if available
			for i, dir := range stagingTVDir.Directories {
				// The first directory is always the requested directory, skip
				// it
				if i == 0 {
					continue
				}

				// Check if sub directory as reach at least 1x redundancy
				if dir.AggregateMinRedundancy <= 1 {
					continue
				}

				// Move Directory to production
				log.Println("move", dir.SiaPath.String(), "to production")
				err = sf.moveToProduction(dir.SiaPath)
				if err != nil {
					log.Println("error moving directory to production:", err)
				}
			}
		}
	}
}

// moveToProduction moves a directory from the staging directory to the
// production directory by renaming it
func (sf *SiaFolder) moveToProduction(dir modules.SiaPath) error {
	// Rebase siapath
	newSiaPath, err := dir.Rebase(getSiaPath(siaStagingDir), getSiaPath(siaProdDir))
	if err != nil {
		return err
	}
	log.Println("Rebased siapath", dir.String(), "to", newSiaPath.String())
	return sf.client.RenterRenamePost(dir, newSiaPath)
}

// eventWatcher continuously listens on the SiaFolder's watcher channels and
// performs the necessary upload/delete operations.
func (sf *SiaFolder) eventWatcher() {
	if sf.watcher == nil {
		return
	}

	for {
		select {
		case <-sf.closeChan:
			return
		case event := <-sf.watcher.Events:
			log.Println("Watcher saw an event")
			filename := filepath.Clean(event.Name)
			f, err := os.Stat(filename)
			if err == nil && f.IsDir() {
				log.Println("Watcher found a directory, adding to watcher and dirs map", filename)
				sf.watcher.Add(filename)
				if _, ok := sf.dirs[filename]; ok {
					// Debug right now, can be removed to clean up code
					log.Println("dir already in the dirs map")
					continue
				}
				sf.dirs[filename] = false
				continue
			}

			// WRITE event, checksum the file and re-upload it if it has changed
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println("Watcher found a write event for:", filename)
				err = sf.handleFileWrite(filename)
				if err != nil {
					log.Println(err)
				}
			}

			// CREATE event
			if event.Op&fsnotify.Create == fsnotify.Create {
				log.Println("Watcher found a create event for:", filename)
				log.Println("file creation detected, uploading", filename)
				uploadRetry(sf, filename)
			}

		case err := <-sf.watcher.Errors:
			if err != nil {
				log.Println("fsevents error:", err)
			}
		}
	}
}

func (sf *SiaFolder) isFile(file string) (bool, error) {
	relpath, err := filepath.Rel(sf.path, file)
	if err != nil {
		return false, fmt.Errorf("error getting relative path: %v", err)
	}

	_, err = sf.client.RenterFileGet(newSiaPath(relpath))
	exists := true
	if err != nil && strings.Contains(err.Error(), "no file known") {
		exists = false
	}
	return exists, nil
}

// handleFileWrite handles a WRITE fsevent.
//
// TODO - need to figure out how to handle these events
func (sf *SiaFolder) handleFileWrite(file string) error {
	checksum, err := checksumFile(file)
	if err != nil {
		return err
	}

	oldChecksum, exists := sf.files[file]
	if exists && oldChecksum != checksum {
		log.Printf("change in %v detected, reuploading..", file)
		sf.files[file] = checksum
		if !sf.archive {
			err = sf.handleRemove(file)
			if err != nil {
				return err
			}
		}
		err = sf.handleCreate(file)
		if err != nil {
			return err
		}
	}

	return nil
}

// Close releases any resources allocated by a SiaFolder.
func (sf *SiaFolder) Close() error {
	close(sf.closeChan)
	if sf.watcher != nil {
		return sf.watcher.Close()
	}
	return nil
}

// handleCreate handles a file creation event. `file` is a relative path to the
// file on disk.
func (sf *SiaFolder) handleCreate(file string) error {
	abspath, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("error getting absolute path to upload: %v", err)
	}
	relpath, err := filepath.Rel(sf.path, file)
	if err != nil {
		return fmt.Errorf("error getting relative path to upload: %v", err)
	}

	log.Println("Uploading", abspath, "as", getSiaPath(relpath))

	if !dryRun {
		err = sf.client.RenterUploadPost(abspath, getSiaPath(relpath), dataPieces, parityPieces)
		if err != nil {
			return fmt.Errorf("error uploading %v: %v", file, err)
		}
	}

	checksum, err := checksumFile(file)
	if err != nil {
		return err
	}
	sf.files[file] = checksum
	return nil
}

// handleRemove handles a file removal event.
func (sf *SiaFolder) handleRemove(file string) error {
	relpath, err := filepath.Rel(sf.path, file)
	if err != nil {
		return fmt.Errorf("error getting relative path to remove: %v", err)
	}

	log.Println("Deleting:", file)

	if !dryRun {
		err = sf.client.RenterDeletePost(getSiaPath(relpath))
		if err != nil {
			return fmt.Errorf("error removing %v: %v", file, err)
		}
	}

	delete(sf.files, file)
	return nil
}

// uploadNonExisting runs once and performs any uploads required to ensure
// every file in files is uploaded to the Sia node.
func (sf *SiaFolder) uploadNonExisting() error {
	log.Println("Getting siafiles")
	renterFiles, err := sf.getSiaFiles()
	if err != nil {
		return err
	}

	for file := range sf.files {
		relpath, err := filepath.Rel(sf.path, file)
		if err != nil {
			return err
		}
		exists := false
		for _, siafile := range renterFiles.Files {
			if siafile.SiaPath.Equals(getSiaPath(relpath)) {
				exists = true
				break
			}
		}
		if !exists {
			err := sf.handleCreate(file)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// uploadChanged runs once and performs any uploads of files where file size in Sia is different from local file
func (sf *SiaFolder) uploadChanged() error {
	renterFiles, err := sf.getStagingSiaFiles()
	if err != nil {
		return err
	}

	// TODO - this for loop can be optimized
	//
	// Submit MR for siasync
	for file := range sf.files {
		relpath, err := filepath.Rel(sf.path, file)
		if err != nil {
			return err
		}
		for _, siafile := range renterFiles.Files {
			if siafile.SiaPath.Equals(getSiaPath(relpath)) {
				sf.files[file] = strconv.FormatInt(int64(siafile.Filesize), 10)
				// set file size to size in Sia and call handleFileWrite
				// if local file has different size it will reload file to Sia
				err := sf.handleFileWrite(file)
				if err != nil {
					return err
				}
				break
			}
		}
	}

	return nil
}

// filters Sia remote files, only files that are in the staging or production
// directories are returned
func (sf *SiaFolder) getSiaFiles() (rf api.RenterFiles, err error) {
	renterFiles, err := sf.client.RenterFilesGet(true)
	if err != nil {
		return rf, err
	}
	for _, siafile := range renterFiles.Files {
		// check for files in the staging and production directories
		staging := !strings.HasPrefix(siafile.SiaPath.Path, siaStagingDir+"/")
		production := !strings.HasPrefix(siafile.SiaPath.Path, siaProdDir+"/")
		if staging || production {
			rf.Files = append(rf.Files, siafile)
		}
	}
	return rf, err
}

// filters Sia remote files, only files that are in the staging directory are returned
func (sf *SiaFolder) getStagingSiaFiles() (rf api.RenterFiles, err error) {
	renterFiles, err := sf.client.RenterFilesGet(true)
	if err != nil {
		return rf, err
	}
	for _, siafile := range renterFiles.Files {
		// check for files in the staging and production directories
		staging := !strings.HasPrefix(siafile.SiaPath.Path, siaStagingDir+"/")
		if staging {
			rf.Files = append(rf.Files, siafile)
		}
	}
	return rf, err
}

// getStagingSiaDir returns the staging directory on sia
func (sf *SiaFolder) getStagingSiaDir(subdir string) (api.RenterDirectory, error) {
	siadir, err := sf.client.RenterGetDir(getSiaPath(filepath.Join(sf.siaStagingDir, movieDir)))
	if err != nil {
		return api.RenterDirectory{}, err
	}

	return siadir, nil
}
