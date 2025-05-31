package cwl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"gopkg.in/ini.v1"
)

const (
	SectionNameProfile = "profile"
)

func LoadAWSConfigs(ctx context.Context, excludeProfiles []string) (map[string]aws.Config, error) {
	f := config.DefaultSharedConfigFilename()

	inif, err := ini.Load(f)
	if err != nil {
		return nil, err
	}

	configs := make(map[string]aws.Config, 0)
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, section := range inif.Sections() {
		if !strings.HasPrefix(section.Name(), SectionNameProfile) {
			continue
		}
		profile := strings.TrimPrefix(section.Name(), SectionNameProfile)
		profile = strings.TrimSpace(profile)
		if slices.Contains(excludeProfiles, profile) {
			continue
		}
		wg.Add(1)
		go func(profile string) {
			defer wg.Done()
			defer func() {
				if err := recover(); err != nil {
					fmt.Println(err)
				}
			}()
			cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile))
			if err != nil {
				return
			}
			_, err = cfg.Credentials.Retrieve(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			configs[profile] = cfg
			mu.Unlock()
		}(profile)
	}
	wg.Wait()

	return configs, nil
}

type Config struct {
	ExcludeProfiles []string `json:"excludeProfiles"`
}

const (
	DotConfigFile = ".cwl.json"
	ConfigFile    = "cwl.json"
)

func LoadDefaultConfig(ctx context.Context) (*Config, error) {

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(ctx, filepath.Join(cwd, DotConfigFile))
	if err == nil {
		return cfg, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg, err = LoadConfig(ctx, filepath.Join(home, DotConfigFile))
	if err == nil {
		return cfg, nil
	}

	configdir := filepath.Join(home, ".config", "cwl")
	cfg, err = LoadConfig(ctx, filepath.Join(configdir, ConfigFile))
	if err == nil {
		return cfg, nil
	}

	return &Config{}, nil
}

func LoadConfig(ctx context.Context, path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := &Config{}
	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
