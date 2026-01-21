package main

import (
	"flag"
	"log"
	"os"

	"github.com/lcpu-club/lfs-auto-grader/internal/config"
	"github.com/lcpu-club/lfs-auto-grader/internal/manager"
)

func defaultValue(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func main() {
	conf := &config.ManagerConfig{}
	conf.Endpoint = flag.String("endpoint", defaultValue(os.Getenv("ENDPOINT"), "https://hpcgame.pku.edu.cn"), "API endpoint")
	conf.RunnerID = flag.String("runner-id", os.Getenv("RUNNER_ID"), "Runner ID")
	conf.RunnerKey = flag.String("runner-key", os.Getenv("RUNNER_KEY"), "Runner Key")

	flag.Parse()

	s := manager.NewManager(conf)

	if err := s.Init(); err != nil {
		log.Fatalln(err)
	}

	if err := s.Start(); err != nil {
		log.Fatalln(err)
	}
}
