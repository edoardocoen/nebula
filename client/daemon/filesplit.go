package daemon

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	util_file "github.com/samoslab/nebula/util/file"
)

const chunkSize int64 = 1024 * 1024
const TEMP_NAMESPACE = "part"

// Slice record file slice for split
type Slice struct {
	Begin int64
	End   int64
}

// DirPair dir and its parent is a pair
type DirPair struct {
	Name   string
	Parent string
	Folder bool
}

// FileSlice split file into slices
func FileSlice(fileName string, chunkSize int64) ([]Slice, error) {
	fileInfo, err := os.Stat(fileName)
	if err != nil {
		return nil, err
	}
	num := int64(math.Ceil(float64(fileInfo.Size()) / float64(chunkSize)))
	sliceArray := make([]Slice, num)
	var i int64
	for ; i < int64(num); i++ {
		if i+1 == num {
			sliceArray[i].Begin = i * chunkSize
			sliceArray[i].End = fileInfo.Size()
		} else {
			sliceArray[i].Begin = i * chunkSize
			sliceArray[i].End = (i+1)*chunkSize - 1
		}
	}

	return sliceArray, nil
}

// FileShardNum calculate sharding number according to chunk size
func FileShardNum(fileName string, chunkSize int64) (int, error) {
	fileInfo, err := os.Stat(fileName)
	if err != nil {
		return 0, err
	}

	num := int(math.Ceil(float64(fileInfo.Size()) / float64(chunkSize)))
	return num, nil
}

// FileSplit split file by size
func FileSplit(outDir, fileName string, fileSize int64, chunkSize, chunkNum int64) ([]string, error) {
	totalSize, err := GetFileSize(fileName)
	if err != nil {
		return nil, err
	}
	if totalSize <= chunkSize {
		return []string{fileName}, nil
	}
	fi, err := os.OpenFile(fileName, os.O_RDONLY, 0644)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	b := make([]byte, chunkSize)
	partFiles := []string{}
	var i int64 = 1
	_, onlyFileName := filepath.Split(fileName)
	for ; i <= int64(chunkNum); i++ {

		fi.Seek((i-1)*(chunkSize), 0)
		if len(b) > int((fileSize - (i-1)*chunkSize)) {
			b = make([]byte, fileSize-(i-1)*chunkSize)
		}

		fi.Read(b)

		filename := filepath.Join(outDir, onlyFileName+"."+TEMP_NAMESPACE+"."+strconv.Itoa(int(i-1)))
		f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		f.Write(b)
		// last part file
		if i == chunkNum && totalSize > chunkNum*chunkSize {
			fi.Seek(i*chunkSize, 0)
			b = make([]byte, fileSize-i*chunkSize)
			fi.Read(b)
			f.Write(b)
		}
		f.Close()
		partFiles = append(partFiles, filename)
	}
	return partFiles, nil
}

// FileJoin join many files into filename
func FileJoin(filename string, partfiles []string) error {
	fii, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer fii.Close()
	for _, file := range partfiles {
		f, err := os.OpenFile(file, os.O_RDONLY, 0644)
		if err != nil {
			return err
		}
		b, err := ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		fii.Write(b)
		f.Close()
	}
	return nil
}

// GetDirsAndFiles returns dirs and files in path root
func GetDirsAndFiles(root string) ([]DirPair, error) {
	dirs := []DirPair{}
	if !util_file.Exists(root) {
		return nil, fmt.Errorf("%s not exists", root)
	}
	err := filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		parent, _ := filepath.Split(path)
		if f.IsDir() {
			dirs = append(dirs, DirPair{Name: f.Name(), Parent: parent, Folder: true})
		} else {
			dirs = append(dirs, DirPair{Name: path, Parent: parent, Folder: false})
		}
		return nil
	},
	)
	if err != nil {
		return nil, err
	}
	return dirs, nil
}

func dirAdjust(dirs []DirPair, parent, dest string, os string) []DirPair {
	seperator := "/"
	if os == "windows" {
		seperator = "\\"
	}
	parent = strings.TrimSuffix(parent, seperator)
	dest = strings.TrimSuffix(dest, seperator)
	// replace parent by dest, parent is D://work, dest = /cloud, D://work/abc.txt -> /cloud/abc.txt
	newDirs := []DirPair{}
	lastFolder := filepath.Base(parent)
	if seperator == "\\" {
		lastFolders := strings.Split(parent, "\\")
		lastFolder = lastFolders[len(lastFolders)-1]
	}
	parentParent := strings.TrimSuffix(parent, seperator+lastFolder)
	for _, dir := range dirs {
		actualDir := strings.Replace(strings.TrimSuffix(dir.Parent, seperator), parentParent, dest, 1)
		if seperator == "\\" {
			actualDir = strings.Replace(actualDir, "\\", "/", -1)
		}
		newDP := DirPair{
			Parent: actualDir,
			Name:   dir.Name,
			Folder: dir.Folder,
		}
		newDirs = append(newDirs, newDP)
	}

	return newDirs
}
