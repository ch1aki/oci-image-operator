package detect

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/takutakahashi/oci-image-operator/actor/github/pkg/github"
)

type Detect struct {
	gh *github.Github
	w  io.Writer
}

func NewDetect(outputFilePath string) (*Detect, error) {
	opt, err := github.GenOpt(nil)
	if err != nil {
		return nil, err
	}
	gh, err := github.Init(opt)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Base(outputFilePath), 0644); err != nil {
		return nil, err
	}
	f, err := os.Create(outputFilePath)
	if err != nil {
		return nil, err
	}
	return &Detect{gh: gh, w: f}, nil
}

func (d *Detect) Execute() error {
	ctx := context.TODO()
	branches, err := d.gh.BranchHash(ctx)
	if err != nil {
		return err
	}
	tags, err := d.gh.TagHash(ctx)
	if err != nil {
		return err
	}
	df := DetectFile{
		Branches:      branches,
		Tags:          tags,
		LastUpdatedAt: time.Now(),
	}
	buf, err := json.Marshal(&df)
	if err != nil {
		return err
	}
	_, err = d.w.Write(buf)
	return err

}

type DetectFile struct {
	Branches      map[string]string `json:"branches"`
	Tags          map[string]string `json:"tags"`
	LastUpdatedAt time.Time         `json:"lastUpdatedAt"`
}
