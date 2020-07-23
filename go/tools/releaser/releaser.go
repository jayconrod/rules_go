// Copyright 2020 The Bazel Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// releaser is a tool for creating rules_go releases.
//
// The rules_go release process is:
//
// * Sync or create the release branch
// * Commit an update to RULES_GO_VERSION in go/def.bzl.
// * (manual) Cherry-pick changes to release.
// * Ensure tests pass locally.
// * Push the release branch.
// * Create or update the GitHub release.
//   * Generate minimal release notes, update boilerplate.
//   * Create an archive.
//   * Upload the archive to mirror.bazel.build.
//   * Upload the archive to the GitHub release.
// * Create or update the announcement PR.
//   * Update boilerplate in README.rst.
// * Create Gazelle update PR.
//   * Update rules_go version in WORKSPACE.
//   * Update boilerplate in README.rst.
//   * Ensure tests pass locally.
// * (manual) Confirm CI passes on release branch.
// * (manual) Submit release and PRs.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v29/github"
	"golang.org/x/oauth2"
)

func main() {
	log.SetPrefix("releaser: ")
	log.SetFlags(0)

	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) (err error) {
	if os.Getenv("BUILD_WORKSPACE_DIRECTORY") != "" {
		return fmt.Errorf("cannot be invoked with 'bazel run'. Copy the binary somewhere and run it directly.")
	}

	fs := flag.NewFlagSet("releaser", flag.ContinueOnError)
	var version, tokenPath string
	var runTests, updateBoilerplate bool
	fs.StringVar(&version, "version", "", "Version to release (for example, 0.2.3)")
	fs.StringVar(&tokenPath, "token", "", "Path to file containing GitHub token")
	fs.BoolVar(&runTests, "test", true, "Whether to run tests")
	fs.BoolVar(&updateBoilerplate, "boilerplate", true, "Whether to update boilerplate in README.rst")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if version == "" {
		return errors.New("-version must be set to a semantic version (for example, 0.2.3)")
	}
	if strings.HasPrefix(version, "v") {
		return errors.New("-version should not start with 'v'")
	}
	m := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(version)
	if m == nil {
		return errors.New("-version must be a semantic version (for example, 0.2.3)")
	}
	major, minor := m[1], m[2]
	if tokenPath == "" {
		return errors.New("-token must be set to a GitHub OAuth token (or a file containing such a token) with permission to create and edit PRs and releases")
	}

	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cacheDir := filepath.Join(userCacheDir, "rules_go_releaser")
	if err := os.MkdirAll(cacheDir, 0777); err != nil {
		return err
	}

	// Find the workspace root directory and ensure there are no pending changes.
	ws, err := getWorkspace()
	if err != nil {
		return err
	}
	if err := checkNoPendingChanges(ws); err != nil {
		return err
	}

	// Create a GitHub client.
	tokenData, err := hex.DecodeString(tokenPath)
	if err != nil {
		// not a raw hex token. Treat as a file path.
		tokenData, err = ioutil.ReadFile(tokenPath)
	}
	if err != nil {
		return err
	}

	ctx := context.Background()
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: string(tokenData)})
	tokenClient := oauth2.NewClient(ctx, tokenSource)
	ghClient := github.NewClient(tokenClient)
	_ = ghClient

	// Sync or create and checkout the release branch.
	releaseBranch := fmt.Sprintf("release-%s.%s", major, minor)
	if err := syncBranch(ws, releaseBranch); err != nil {
		return err
	}

	// Ensure RULES_GO_VERSION is set. If not, add a commit setting it, then
	// stop and give the user a chance to cherry-pick changes they want.
	oldVersion, err := getRulesGoVersion(ws)
	if err != nil {
		return err
	}
	if oldVersion != version {
		if err := setRulesGoVersionAndCommit(ws, version); err != nil {
			return err
		}
		log.Print("RULES_GO_VERSION has been set and commited on the release branch.\nCherry-pick changes you want, then re-run this command.")
		return nil
	}
	if haveCommits, err := haveCommitsSinceVersionSet(ws, version); err != nil {
		return err
	} else if !haveCommits {
		return fmt.Errorf("no commits on release branch since RULES_GO_VERSION was set. Cherry-pick changes you want, then re-run this command.")
	}

	// Check that there isn't already a release with that tag.
	release, err := findRelease(ctx, ghClient, version)
	var rerr *releaseNotFoundError
	if err != nil && !errors.As(err, &rerr) {
		return err
	}
	if release != nil && !release.GetDraft() {
		return fmt.Errorf("version %s was already released", version)
	}

	// Check that all tests pass.
	if runTests {
		log.Printf("running tests...")
		cmd := exec.Command("bazel", "test", "//...")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Dir = ws
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Push the release branch.
	log.Printf("pushing release branch %s...", releaseBranch)
	if err := pushBranch(ws, releaseBranch); err != nil {
		return err
	}

	// Create a release archive.
	archName := fmt.Sprintf("rules_go-v%s.tar.gz", version)
	archPath := filepath.Join(cacheDir, archName)
	log.Printf("creating release archive at %s...", archPath)
	if err := createArchive(ws, releaseBranch, archPath); err != nil {
		return err
	}
	archHash, err := hashFile(archPath)
	if err != nil {
		return err
	}

	// Upload the release archive to mirror.bazel.build.
	log.Printf("uploading archive to mirror.bazel.build...")
	if err := uploadToMirror(ws, archPath, version); err != nil {
		return err
	}

	// Create a GitHub release.
	log.Printf("updating draft GitHub release...")
	release, err = updateRelease(ctx, ghClient, release, version, releaseBranch, archPath, archHash)
	if err != nil {
		return err
	}

	// Update boilerplate.
	var boilerplatePR *github.PullRequest
	var boilerplateMsg string
	if updateBoilerplate {
		log.Printf("updating boilerplate...")
		boilerplateBranchName := "update-boilerplate"
		var err error
		pr, err := findPRForBranch(ctx, ghClient, boilerplateBranchName)
		var notFoundErr *prNotFoundError
		if err != nil && !errors.As(err, &notFoundErr) {
			return err
		}

		boilerplateBranchExists := branchExists(ws, boilerplateBranchName)
		if !boilerplateBranchExists {
			if err := createBranch(ws, boilerplateBranchName, "master"); err != nil {
				return err
			}
		}
		if err := checkoutBranch(ws, boilerplateBranchName); err != nil {
			return err
		}
		readmePath := filepath.Join(ws, "README.rst")
		oldReadmeData, err := ioutil.ReadFile(readmePath)
		if err != nil {
			return err
		}
		readmeData := []byte(editBoilerplate(string(oldReadmeData), version, archHash))
		if !bytes.Equal(readmeData, oldReadmeData) {
			if err := ioutil.WriteFile(readmePath, readmeData, 0666); err != nil {
				return err
			}
			message := fmt.Sprintf("update boilerplate for %s [skip ci]", version)
			if err := createCommit(ws, message); err != nil {
				return err
			}
			if err := pushBranch(ws, boilerplateBranchName); err != nil {
				return err
			}
			if pr == nil {
				if _, err := createPR(ctx, ghClient, message, boilerplateBranchName, "master"); err != nil {
					return err
				}
			}
		}

		boilerplateMsg = fmt.Sprintf("- Squash and merge boilerplate PR at %s\n", boilerplatePR.GetIssueURL())
	}

	testURL := fmt.Sprintf("https://buildkite.com/bazel/rules-go-golang/builds?branch=%s", releaseBranch)
	releaseURL := release.GetHTMLURL()
	log.Printf(`release is ready to go, but there are several manual steps:
- Verify CI passes at %s
- Edit and publish release notes at %s
%s
TODO:
- Update boilerplate in Gazelle`,
		testURL, releaseURL, boilerplateMsg)

	return nil
}

// git operations
// --------------

func checkNoPendingChanges(dir string) error {
	if err := runForStatus(dir, "git", "diff-index", "--quiet", "HEAD"); err != nil {
		var xerr *exec.ExitError
		if errors.As(err, &xerr) && xerr.ExitCode() == 1 {
			return errors.New("repository has pending changes. Check everything in first.")
		}
		return err
	}
	return nil
}

func branchExists(dir, name string) bool {
	err := runForStatus(dir, "git", "rev-parse", "--verify", name)
	return err == nil
}

func syncBranch(dir, name string) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("syncing branch %s: %w", name, err)
		}
	}()
	localErr := runForStatus(dir, "git", "rev-parse", "--verify", name)
	if localErr == nil {
		// Already have branch.
		if err := runForStatus(dir, "git", "checkout", name); err != nil {
			return err
		}
		return runForStatus(dir, "git", "pull", "--ff-only", "origin", name)
	} else {
		// Need to fetch branch.
		if err := runForStatus(dir, "git", "fetch", "origin", name); err != nil {
			return err
		}
		return runForStatus(dir, "git", "checkout", name, "origin/"+name)
	}
}

func checkoutBranch(dir, name string) error {
	return runForStatus(dir, "git", "switch", name)
}

func createBranch(dir, name, base string) error {
	return runForStatus(dir, "git", "branch", name, base)
}

func createCommit(dir, message string) error {
	return runForStatus(dir, "git", "commit", "-a", "-m", message)
}

func createArchive(dir, releaseBranch, outPath string) error {
	if err := runForStatus(dir, "git", "archive", "--output="+outPath, releaseBranch); err != nil {
		return fmt.Errorf("could not create archive: %w", err)
	}
	return nil
}

func pushBranch(dir, name string) error {
	out, err := runForOutput(dir, "git", "rev-parse", "origin/"+name, name)
	if err == nil {
		// Already have remote branch. If they're the same, don't push.
		out = bytes.TrimSpace(out)
		i := bytes.Index(out, []byte("\n"))
		if i < 0 {
			return fmt.Errorf("could not parse git rev-parse output:\n%s", out)
		}
		remote, local := string(out[:i]), string(out[i+1:])
		if remote == local {
			return nil
		}
	}

	if err := runForStatus(dir, "git", "push", "origin", name); err != nil {
		return fmt.Errorf("could not push branch %s: %w", name, err)
	}
	return nil
}

// file operations
// ---------------

var rulesGoVersionRe = regexp.MustCompile(`(?m)^RULES_GO_VERSION = "([^"]*)"$`)

func getRulesGoVersion(dir string) (string, error) {
	path := filepath.Join(dir, "go/def.bzl")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := rulesGoVersionRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("%s: RULES_GO_VERSION not found", path)
	}
	return string(m[1]), nil
}

func setRulesGoVersionAndCommit(dir, version string) error {
	path := filepath.Join(dir, "go/def.bzl")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	data = rulesGoVersionRe.ReplaceAll(data, []byte(fmt.Sprintf(`RULES_GO_VERSION = "%s"`, version)))
	if err := ioutil.WriteFile(path, data, 0666); err != nil {
		return err
	}
	if err := runForStatus(dir, "git", "commit", "-am", fmt.Sprintf("Set RULES_GO_VERSION to %s", version)); err != nil {
		return err
	}
	return nil
}

func haveCommitsSinceVersionSet(dir, version string) (bool, error) {
	out, err := runForOutput(dir, "git", "log", "--format=%s", "-1")
	if err != nil {
		return false, err
	}
	msg := strings.TrimSpace(string(out))
	return msg != fmt.Sprintf("Set RULES_GO_VERSION to %s", version), nil
}

func hashFile(path string) (hexHash string, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("error hashing file %s: %w", path, err)
		}
	}()

	r, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func editBoilerplate(text, version, archHash string) string {
	tag := "v" + version
	releaseURL := fmt.Sprintf("https://github.com/bazelbuild/rules_go/releases/download/%[1]s/rules_go-%[1]s.tar.gz", tag)
	mirrorURL := "https://mirror.bazel.build/" + releaseURL[len("https://"):]
	newRule := []string{
		"http_archive(",
		`    name = "io_bazel_rules_go",`,
		`    sha256 = "` + archHash + `",`,
		`    urls = [`,
		`        "` + mirrorURL + `",`,
		`        "` + releaseURL + `",`,
		`    ],`,
		`)`,
	}

	re := regexp.MustCompile(`(?m)([ \t]*)http_archive\(\s*name = "io_bazel_rules_go"(?:[^)]*download/([0-9.]+)/)?[^)]*\)\n`)
	ms := re.FindAllStringSubmatchIndex(text, -1)
	const (
		allGroup    = 0
		indentGroup = 1
		tagGroup    = 2
	)
	b := &strings.Builder{}
	pos := 0
	for _, m := range ms {
		if m[2*tagGroup] >= 0 {
			oldTag := text[m[2*tagGroup]:m[2*tagGroup+1]]
			if compareSemver(oldTag, tag) > 0 {
				// Old version is newer. Keep the old boilerplate.
				b.WriteString(text[pos:m[allGroup+1]])
				pos = m[allGroup+1]
				continue
			}
		}

		if pos < m[allGroup] {
			b.WriteString(text[pos:m[allGroup]])
		}
		indent := text[m[2*indentGroup]:m[2*indentGroup+1]]
		for _, line := range newRule {
			b.WriteString(indent)
			b.WriteString(line)
			b.WriteString("\n")
		}
		pos = m[1]
	}
	if pos < len(text) {
		b.WriteString(text[pos:])
	}
	return b.String()
}

// GitHub operations
// -----------------

func updateRelease(ctx context.Context, ghClient *github.Client, release *github.RepositoryRelease, version, branchName, archPath, archHash string) (updatedRelease *github.RepositoryRelease, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("could not update relase: %w", err)
		}
	}()
	tag := "v" + version

	// Create or edit the release.
	const boilerplateSkel = `## WORKSPACE code

    load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

    http_archive(name = "io_bazel_rules_go")

    load("@io_bazel_rules_go//go:deps.bzl", "go_rules_dependencies", "go_register_toolchains")

    go_rules_dependencies()

    go_register_toolchains()
`
	if release == nil {
		// Create a new release.
		body := editBoilerplate(boilerplateSkel, version, archHash)
		t := true
		newRelease := &github.RepositoryRelease{
			TagName:         &tag,
			TargetCommitish: &branchName,
			Name:            &tag,
			Body:            &body,
			Draft:           &t,
			Prerelease:      &t,
		}
		release, _, err = ghClient.Repositories.CreateRelease(ctx, "bazelbuild", "rules_go", newRelease)
		if err != nil {
			return nil, err
		}
	} else {
		// Update an existing release.
		var oldBody string
		if release.Body == nil {
			oldBody = boilerplateSkel
		} else {
			oldBody = *release.Body
		}
		body := editBoilerplate(oldBody, version, archHash)
		if body != oldBody {
			release.Body = &body
			release, _, err = ghClient.Repositories.EditRelease(ctx, "bazelbuild", "rules_go", *release.ID, release)
			if err != nil {
				return nil, err
			}
		}
		for _, asset := range release.Assets {
			_, err := ghClient.Repositories.DeleteReleaseAsset(ctx, "bazelbuild", "rules_go", *asset.ID)
			if err != nil {
				return nil, err
			}
		}
	}

	// Upload the archive.
	archFile, err := os.Open(archPath)
	if err != nil {
		return nil, err
	}
	defer archFile.Close()
	upload := &github.UploadOptions{
		Name:      fmt.Sprintf("rules_go-%s.tar.gz", tag),
		MediaType: "application/gzip",
	}
	if _, _, err := ghClient.Repositories.UploadReleaseAsset(ctx, "bazelbuild", "rules_go", *release.ID, upload, archFile); err != nil {
		return nil, err
	}

	return release, nil
}

type releaseNotFoundError struct {
	version string
}

func (e *releaseNotFoundError) Error() string {
	return fmt.Sprintf("release %s not found", e.version)
}

func findRelease(ctx context.Context, ghClient *github.Client, version string) (release *github.RepositoryRelease, err error) {
	tag := "v" + version
	opts := &github.ListOptions{}
	for {
		releases, resp, err := ghClient.Repositories.ListReleases(ctx, "bazelbuild", "rules_go", opts)
		if err != nil {
			return nil, err
		}
		for _, r := range releases {
			if r.GetName() == tag {
				return r, nil
			}
		}
		if opts.Page+1 > resp.LastPage {
			return nil, &releaseNotFoundError{version: version}
		}
		opts.Page = resp.NextPage
	}
}

type prNotFoundError struct {
	name string
}

func (e *prNotFoundError) Error() string {
	return fmt.Sprintf("no PR found with branch name %s", e.name)
}

func findPRForBranch(ctx context.Context, ghClient *github.Client, name string) (*github.PullRequest, error) {
	prs, _, err := ghClient.PullRequests.List(ctx, "bazelbuild", "rules_go", &github.PullRequestListOptions{
		State: "open",
		Head:  "rules_go:" + name,
	})
	if err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, &prNotFoundError{name: name}
	}
	if len(prs) > 1 {
		return nil, fmt.Errorf("found multiple open PRs with branch name %s", name)
	}
	return prs[0], nil
}

func createPR(ctx context.Context, ghClient *github.Client, title, branchName, base string) (*github.PullRequest, error) {
	pr, _, err := ghClient.PullRequests.Create(ctx, "bazelbuild", "rules_go", &github.NewPullRequest{
		Title: &title,
		Head:  &branchName,
		Base:  &base,
	})
	return pr, err
}

// misc
// ----

func getWorkspace() (string, error) {
	if ws := os.Getenv("BUILD_WORKSPACE_DIRECTORY"); ws != "" {
		return ws, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	ws, err := runForOutput(wd, "bazel", "info", "workspace")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ws)), nil
}

func uploadToMirror(dir, path, version string) error {
	url := fmt.Sprintf("gs://bazel-mirror/github.com/bazelbuild/rules_go/releases/download/v%[1]s/rules_go-v%[1]s.tar.gz", version)
	if err := runForStatus(dir, "gsutil", "cp", path, url); err != nil {
		return fmt.Errorf("could not upload to mirror: %w", err)
	}
	return nil
}

func runForStatus(dir, path string, args ...string) error {
	cmd := exec.Command(path, args...)
	buf := &bytes.Buffer{}
	cmd.Stderr = buf
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s %s: %w\n%s", path, strings.Join(args, " "), err, buf.Bytes())
		}
		return fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
	}
	return nil
}

func runForOutput(dir, path string, args ...string) ([]byte, error) {
	cmd := exec.Command(path, args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s %s: %w\n%s", path, strings.Join(args, " "), err, stderr.Bytes())
		}
		return nil, fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
	}
	return stdout, nil
}

var semverRe = regexp.MustCompile(`v?([0-9]+)\.([0-9]+)\.([0-9]+)`)

func compareSemver(a, b string) int {
	ma := semverRe.FindStringSubmatch(a)
	mb := semverRe.FindStringSubmatch(b)
	if ma == nil && mb == nil {
		return 0
	}
	if ma == nil {
		return -1
	}
	if mb == nil {
		return +1
	}

	for i := 1; i <= 3; i++ {
		va, _ := strconv.Atoi(ma[i])
		vb, _ := strconv.Atoi(mb[i])
		if c := va - vb; c != 0 {
			return c
		}
	}
	return 0
}
