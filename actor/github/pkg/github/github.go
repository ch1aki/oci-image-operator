package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Netflix/go-env"
	"github.com/google/go-github/v43/github"
	"github.com/sirupsen/logrus"
	"github.com/takutakahashi/oci-image-operator/actor/base/pkg/detect"
	"golang.org/x/oauth2"
)

type GithubOpt struct {
	BaseURL             string `env:"GITHUB_API_URL,default=https://api.github.com/"`
	Org                 string `env:"GITHUB_ORG,required=true"`
	Repo                string `env:"GITHUB_REPO,required=true"`
	Branches            string `env:"TARGET_BRANCHES"`
	Tags                string `env:"TARGET_TAGS"`
	PersonalAccessToken string `env:"GITHUB_TOKEN"`
	WorkflowFileName    string `env:"GITHUB_WORKFLOW_FILENAME,default=build.yaml"`
	HTTPClient          *http.Client
}

type Github struct {
	c        *github.Client
	opt      *GithubOpt
	branches []string
	tags     []string
	revs     map[string]string
}

func Init(opt *GithubOpt) (*Github, error) {
	if opt.Org == "" {
		newOpt, err := GenOpt(opt.HTTPClient)
		if err != nil {
			return nil, err
		}
		opt = newOpt
	}
	if opt.HTTPClient == nil {
		httpcli := &http.Client{}
		if opt.PersonalAccessToken != "" {
			ts := oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: opt.PersonalAccessToken},
			)
			httpcli = oauth2.NewClient(context.Background(), ts)
		}
		opt.HTTPClient = httpcli
	}
	c := github.NewClient(opt.HTTPClient)
	baseURL, err := url.Parse(opt.BaseURL)
	if err != nil {
		return nil, err
	}
	c.BaseURL = baseURL
	b, t := []string{}, []string{}
	if opt.Branches != "" {
		b = strings.Split(opt.Branches, ",")
	}
	if opt.Tags != "" {
		t = strings.Split(opt.Tags, ",")
	}
	return &Github{c: c, opt: opt, branches: b, tags: t, revs: map[string]string{}}, nil
}

func GenOpt(httpClient *http.Client) (*GithubOpt, error) {
	var opt GithubOpt
	_, err := env.UnmarshalFromEnviron(&opt)
	if err != nil {
		return nil, err
	}
	opt.HTTPClient = httpClient
	return &opt, err
}

func (g Github) BranchHash(ctx context.Context) (map[string]string, error) {
	if len(g.branches) == 0 {
		return map[string]string{}, nil
	}
	for _, b := range g.branches {
		branch, _, err := g.c.Repositories.GetBranch(
			ctx, g.opt.Org, g.opt.Repo, b, true)
		if err != nil {
			return nil, err
		}
		g.setBranchHash(b, branch.GetCommit().GetSHA())
	}
	return g.getBranchHashes(), nil
}
func (g Github) TagHash(ctx context.Context) (map[string]string, error) {
	if len(g.tags) == 0 {
		return map[string]string{}, nil
	}
	tags, _, err := g.c.Repositories.ListTags(
		ctx, g.opt.Org, g.opt.Repo, &github.ListOptions{})
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return map[string]string{}, nil
	}
	for _, t := range g.tags {
		if t == detect.MapKeyLatestTagHash {
			g.setTagHash(detect.MapKeyLatestTagHash, tags[0].GetCommit().GetSHA())
			break
		}
		if t == detect.MapKeyLatestTagName {
			g.setTagHash(detect.MapKeyLatestTagName, tags[0].GetName())
			break
		}
		for _, tag := range tags {
			if tag.GetName() == t {
				g.setTagHash(t, tag.GetCommit().GetSHA())
			}
		}
	}
	return g.getTagHashes(), nil
}

func (g *Github) setBranchHash(branch, hash string) {
	g.setHash("branch", branch, hash)
}
func (g *Github) setTagHash(tag, hash string) {
	g.setHash("tag", tag, hash)
}

func (g *Github) setHash(t, v, hash string) {
	g.revs[fmt.Sprintf("%s/%s", t, v)] = hash

}

func (g *Github) getBranchHashes() map[string]string {
	return g.getHashes("branch")
}

func (g *Github) getTagHashes() map[string]string {
	return g.getHashes("tag")
}

func (g *Github) getHashes(t string) map[string]string {
	ret := map[string]string{}
	for k, v := range g.revs {
		if strings.Contains(k, t) {
			ret[strings.TrimLeft(k, fmt.Sprintf("%s/", t))] = v
		}
	}
	return ret

}

func (g *Github) Dispatch(ctx context.Context, ref string, wait bool) error {
	branch, _, _ := g.c.Repositories.GetBranch(
		ctx,
		g.opt.Org,
		g.opt.Repo,
		"master",
		false,
	)
	if branch == nil {
		var err error = nil
		branch, _, err = g.c.Repositories.GetBranch(
			ctx,
			g.opt.Org,
			g.opt.Repo,
			"main",
			false,
		)
		if err != nil {
			return err
		}
	}
	if branch == nil {
		return fmt.Errorf("default branch must be main or master")
	}

	res, err := g.c.Actions.CreateWorkflowDispatchEventByFileName(
		ctx,
		g.opt.Org,
		g.opt.Repo,
		g.opt.WorkflowFileName,
		github.CreateWorkflowDispatchEventRequest{
			Ref: branch.GetName(),
			Inputs: map[string]interface{}{
				"revision": ref,
			},
		},
	)
	if err != nil {
		return err
	}
	if res.StatusCode != 204 {
		return fmt.Errorf("dispatch failed: %s", res.Status)
	}
	if wait {
		return g.waitForComplete(ctx)
	}
	return nil
}

func (g *Github) waitForComplete(ctx context.Context) error {
	var ourRun *github.WorkflowRun
	for {
		time.Sleep(2 * time.Second)
		runs, _, err := g.c.Actions.ListWorkflowRunsByFileName(
			ctx,
			g.opt.Org,
			g.opt.Repo,
			g.opt.WorkflowFileName,
			&github.ListWorkflowRunsOptions{
				Status: "queued",
				ListOptions: github.ListOptions{
					PerPage: 1,
				},
			},
		)
		if err != nil {
			return err
		}
		if len(runs.WorkflowRuns) == 0 {
			continue
		}
		logrus.Infof("id: %d", runs.WorkflowRuns[0].GetID())
		logrus.Debug(time.Since(runs.WorkflowRuns[0].GetCreatedAt().Time))
		if time.Since(runs.WorkflowRuns[0].GetCreatedAt().Time) > 10*time.Second {
			continue
		}
		ourRun = runs.WorkflowRuns[0]
		break
	}
	for {
		time.Sleep(3 * time.Second)
		run, _, err := g.c.Actions.GetWorkflowRunByID(
			ctx,
			g.opt.Org,
			g.opt.Repo,
			ourRun.GetID(),
		)
		if err != nil {
			return err
		}
		logrus.Info(run.GetConclusion())
		switch run.GetConclusion() {
		case "success":
			return nil
		case "failure":
			return fmt.Errorf("failure")
		default:
			continue
		}
	}
}
