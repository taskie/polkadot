package main

import (
	"container/list"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"io/ioutil"
	"gopkg.in/yaml.v2"
	"path/filepath"
)

type PathConf struct {
	Type string
	Path string
}

func readEntryTags(entryTagsPath string) (entryTags []string) {
	buf, err := ioutil.ReadFile(entryTagsPath)
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal(buf, &entryTags)
	if err != nil {
		panic(err)
	}
	return
}

func searchPaths(pathConfMap map[string]PathConf) (fullPathMap map[string]string) {
	fullPathMap = make(map[string]string)
	for path, conf := range pathConfMap {
		if conf.Type == "exec" {
			if fullPath, err := exec.LookPath(path); err == nil {
				fullPathMap[path] = fullPath
			}
		} else if conf.Type == "dir" {
			if _, err := os.Stat(path); err == nil {
				if fullPath, err := filepath.Abs(path); err == nil {
					fullPathMap[path] = fullPath
				}

			}
		}
	}
	return
}

func readPathConfs(polkaDirPaths []string) (fullPathMap map[string]string) {
	fullPathMap = make(map[string]string)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/paths.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var pathConfMap map[string]PathConf
		err = yaml.Unmarshal(buf, &pathConfMap)
		if err != nil {
			panic(err)
		}
		for path, fullPath := range searchPaths(pathConfMap) {
			fullPathMap[path] = fullPath
		}
	}
	return
}

func readTagConfs(polkaDirPaths []string) (allTagConfMap map[string][]string) {
	allTagConfMap = make(map[string][]string)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/tags.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var tagConfMap map[string][]string
		err = yaml.Unmarshal(buf, &tagConfMap)
		if err != nil {
			panic(err)
		}
		for tag, children := range tagConfMap {
			allTagConfMap[tag] = children // overwrite
		}
	}
	return
}

func resolveTags(tagConf map[string][]string, entryTags []string) (acceptTags []string, rejectTags []string) {
	queue := list.New()
	for _, tag := range entryTags {
		queue.PushBack(tag)
	}
	idx := 0
	checked := make(map[string]int) // tag -> idx
	removed := make(map[string]int) // tag -> idx
	for queue.Len() > 0 {
		tag := queue.Remove(queue.Front()).(string)
		if _, ok := checked[tag]; ok {
			continue
		} else if _, ok := removed[tag]; ok {
			continue
		}
		removeFlag := tag[0] == '!'
		if removeFlag {
			removed[tag[1:]] = idx
		} else {
			checked[tag] = idx
		}
		newTags := tagConf[tag]
		for _, newTag := range newTags {
			if removeFlag {
				queue.PushBack("!" + newTag[1:])
			} else {
				queue.PushBack(newTag)
			}
		}
		idx += 1
	}
	acceptTags = make([]string, len(checked))
	for tag, _ := range checked {
		acceptTags = append(acceptTags, tag)
	}
	rejectTags = make([]string, len(removed))
	for tag, _ := range removed {
		rejectTags = append(rejectTags, tag)
	}
	return
}

func main() {
	currentDirPath, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	flag.Parse()
	entryTagsPath := flag.Arg(0)
	polkaDirPaths := flag.Args()[1:]

	fmt.Println("dotfiles dir: " + currentDirPath)

	fmt.Println("entry tags path: " + entryTagsPath)
	entryTags := readEntryTags(entryTagsPath)
	fmt.Println(entryTags)

	tagMap := readPathConfs(polkaDirPaths)
	fmt.Println(tagMap)

	tagConf := readTagConfs(polkaDirPaths)
	// fmt.Println(tagConf)

	acceptTags, rejectTags := resolveTags(tagConf, entryTags)

	for _, tag := range acceptTags {
		if _, ok := tagMap[tag]; !ok {
			tagMap[tag] = tag
		}
	}

	for _, tag := range rejectTags {
		delete(tagMap, tag)
	}

	fmt.Println(tagMap)
}
