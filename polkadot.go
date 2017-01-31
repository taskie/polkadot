package main

import (
	"container/list"
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

type PathConf struct {
	Type string
	Path string
}

func readEntryTags(entryTagsPath string) (entryTags map[string]string) {
	buf, err := ioutil.ReadFile(entryTagsPath)
	if err != nil {
		panic(err)
	}
	err = yaml.Unmarshal(buf, &entryTags)
	if err != nil {
		panic(err)
	}
	for k, v := range entryTags {
		if v == "" {
			entryTags[k] = k
		}
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

func readTagConfs(polkaDirPaths []string) (allTagConfMap map[string]map[string]string) {
	allTagConfMap = make(map[string]map[string]string)
	for _, dirPath := range polkaDirPaths {
		confPath := dirPath + "/tags.yml"
		if _, err := os.Stat(confPath); err != nil {
			continue
		}
		buf, err := ioutil.ReadFile(confPath)
		if err != nil {
			panic(err)
		}
		var tagConfMap map[string]map[string]string
		err = yaml.Unmarshal(buf, &tagConfMap)
		if err != nil {
			panic(err)
		}
		for tag, children := range tagConfMap {
			for k, v := range children {
				if v == "" {
					children[k] = k
				}
			}
			allTagConfMap[tag] = children // overwrite
		}
	}
	return
}

func resolveTags(tagConf map[string]map[string]string, entryTags map[string]string) (
	acceptTags map[string]string, rejectTags map[string]string) {
	queue := list.New()
	for k, v := range entryTags {
		queue.PushBack([]string{k, v})
	}
	acceptTags = make(map[string]string)
	rejectTags = make(map[string]string)
	for queue.Len() > 0 {
		kv := queue.Remove(queue.Front()).([]string)
		tag := kv[0]
		value := kv[1]
		if _, ok := acceptTags[tag]; ok {
			continue
		} else if _, ok := acceptTags[tag]; ok {
			continue
		}
		removeFlag := tag[0] == '!'
		if removeFlag {
			rejectTags[tag[1:]] = value
		} else {
			acceptTags[tag] = value
		}
		newTags := tagConf[tag]
		for newTag, v := range newTags {
			if removeFlag {
				queue.PushBack([]string{"!" + newTag[1:], v})
			} else {
				queue.PushBack([]string{newTag, v})
			}
		}
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

	acceptTags, rejectTags := resolveTags(tagConf, entryTags)
	for tag, value := range acceptTags {
		tagMap[tag] = value
	}
	for tag, _ := range rejectTags {
		delete(tagMap, tag)
	}

	fmt.Println(tagMap)
}
