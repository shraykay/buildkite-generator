package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"

	gh "github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"

	bk "github.com/buildkite/go-buildkite/v2/buildkite"
	"github.com/urfave/cli/v2"
)

var (
	// version defines the current version
	version string
)

const (
	org    = "bluecore-inc"
	gitOrg = "TriggerMail"
)

func main() {
	app := cli.NewApp()
	app.EnableBashCompletion = true
	app.Name = "buildkite-generator"
	app.Description = "utility tool to generate kubernetes manifests"
	app.Flags = []cli.Flag{}
	app.Version = version
	app.Commands = []*cli.Command{
		{
			Name:   "create",
			Usage:  "create a set of manifests - buildkite-generator create <repo name>",
			Action: createAction,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "token",
					Usage:    "buildkite api token to create pipeline",
					EnvVars:  []string{"BUILDKITE_API_TOKEN"},
					Required: true,
				},
				&cli.StringFlag{
					Name:     "github-token",
					Usage:    "github token for branch protection",
					EnvVars:  []string{"GITHUB_API_TOKEN"},
					Required: true,
				},
			},
		},
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func createAction(c *cli.Context) error {
	name := c.Args().Get(0)
	if name == "" {
		return fmt.Errorf("could not find project name")
	}

	client, err := New(c)
	if err != nil {
		return err
	}
	err = client.CreatePipeline(name)
	if err != nil {
		return err
	}
	err = client.CreateBranchProtections(name)
	if err != nil {
		return err
	}
	err = client.InitPipelineFile(name)
	return err
}

type Client struct {
	buildkite *bk.Client
	github    *gh.Client
}

func New(c *cli.Context) (*Client, error) {
	// setup buildkite client
	config, err := bk.NewTokenConfig(c.String("token"), false)
	if err != nil {
		return nil, err
	}

	// setup github client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.String("github-token")})
	tc := oauth2.NewClient(ctx, ts)

	return &Client{
		buildkite: bk.NewClient(config.Client()),
		github:    gh.NewClient(tc),
	}, err
}

func (b *Client) CreatePipeline(name string) error {
	pl := bk.CreatePipeline{
		Name:       name,
		Repository: fmt.Sprintf("git@github.com:TriggerMail/%s.git", name),
		Steps: []bk.Step{
			{
				Type:    pstring("script"),
				Name:    pstring(":pipeline:"),
				Command: pstring("buildkite-agent pipeline upload"),
			},
		},
		DefaultBranch: "master",
		Description:   fmt.Sprintf("pipeline for %s", name),
		ProviderSettings: &bk.GitHubSettings{
			TriggerMode:                             pstring("code"),
			BuildPullRequests:                       pbool(true),
			SkipPullRequestBuildsForExistingCommits: pbool(true),
			PublishCommitStatus:                     pbool(true),
			Repository:                              pstring(fmt.Sprintf("TriggerMail/%s", name)),
		},
		CancelRunningBranchBuilds:       true,
		CancelRunningBranchBuildsFilter: "!master",
	}

	_, _, err := b.buildkite.Pipelines.Create(org, &pl)
	if err != nil {
		return fmt.Errorf("could not create pipeline: %w", err)
	}

	_, err = b.buildkite.Pipelines.AddWebhook(org, name)
	return err
}

func pbool(b bool) *bool {
	return &b
}

func pstring(s string) *string {
	return &s
}

func (b *Client) CreateBranchProtections(name string) error {
	ctx := context.Background()
	p, _, err := b.github.Repositories.GetBranchProtection(ctx, gitOrg, name, "master")

	if err != nil {
		return fmt.Errorf("could not retrieve branch protection from github: %w", err)
	}

	// add our new buildkite status check to existing set
	c := append(p.RequiredStatusChecks.Contexts, fmt.Sprintf("buildkite/%s", name))
	_, _, err = b.github.Repositories.UpdateBranchProtection(ctx, gitOrg, name, "master", &gh.ProtectionRequest{
		RequiredStatusChecks: &gh.RequiredStatusChecks{
			Contexts: c,
		},
	},
	)

	return err
}

func (b *Client) InitPipelineFile(name string) error {
	file, err := yaml.Marshal(Pipeline{
		Name:        name,
		Description: fmt.Sprintf("%s build pipeline", name),
		Steps: []PipelineStep{
			{
				Command: "buildkite-agent pipeline upload",
				Label:   ":pipeline:",
			},
		},
	})
	if err != nil {
		return err
	}

	err = os.MkdirAll(".buildkite", 0755)
	if err != nil {
		return fmt.Errorf("could not create directory for template file: %w", err)
	}

	fmt.Println("reminder: create a PR with the template.yaml to the repository!")

	return ioutil.WriteFile(".buildkite/template.yaml", file, 0755)
}

type Pipeline struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []PipelineStep `yaml:"steps"`
}

type PipelineStep struct {
	Command string `yaml:"command"`
	Label   string `yaml:"label"`
}
