package main

import (
	"os"
	"path/filepath"

	"github.com/emersion/go-imap/v2/imapclient"
)

var (
	User     = ""
	Password = ""
	Host     = ""
)

var WhiteList = []string{
	"ortel.us",
	"redhat.com",
	"gmail.com",
	"gmx.com",
	"fidelity.com",
	"*.fidelity.com",
	"*.delta.com",
	"*.aa.com",
	"*.amazon.com",
}

var BlackList = []string{
	"new.freshtravellernews.com",
}

func init() {
	User = os.Getenv("USER")
	Password = os.Getenv("PASSWORD")
	Host = os.Getenv("HOST")
}

func inBlackList(host string) (matched bool) {
	var err error
	for _, pattern := range BlackList {
		matched, err = filepath.Match(pattern, host)
		if err != nil {
			panic(err)
		}
		if matched {
			break
		}
	}
	return
}

func inWhiteList(host string) (matched bool) {
	var err error
	for _, pattern := range WhiteList {
		matched, err = filepath.Match(pattern, host)
		if err != nil {
			panic(err)
		}
		if matched {
			break
		}
	}
	return
}

func main() {
	client, err := imapclient.DialTLS(Host, nil)
	if err != nil {
		panic(err)
	}
	cmd := client.Login(User, Password)
	err = cmd.Wait()
	if err != nil {
		panic(err)
	}

	filter := Filter{
		client:     client,
		promptUser: true,
	}
	filter.Run()
}
