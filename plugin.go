package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	version "github.com/hashicorp/go-version"
)

// Plugin defines the Helm plugin parameters.
type Plugin struct {
	Debug        bool     `envconfig:"DEBUG"`
	ShowEnv      bool     `envconfig:"SHOW_ENV"`
	Wait         bool     `envconfig:"WAIT"`
	Recreate     bool     `envconfig:"RECREATE_PODS" default:"false"`
	WaitTimeout  uint32   `envconfig:"WAIT_TIMEOUT" default:"300"`
	Actions      []string `envconfig:"ACTIONS" required:"true"`
	AuthKey      string   `envconfig:"AUTH_KEY"`
	KeyPath      string   `envconfig:"KEY_PATH"`
	Zone         string   `envconfig:"ZONE"`
	Region       string   `envconfig:"REGION"`
	Cluster      string   `envconfig:"CLUSTER"`
	Project      string   `envconfig:"PROJECT"`
	Namespace    string   `envconfig:"NAMESPACE"`
	ChartRepo    string   `envconfig:"CHART_REPO"`
	Bucket       string   `envconfig:"BUCKET"`
	ChartPath    string   `envconfig:"CHART_PATH" required:"true"`
	ChartVersion string   `envconfig:"CHART_VERSION"`
	Release      string   `envconfig:"RELEASE"`
	Package      string   `envconfig:"PACKAGE"`
	Values       []string `envconfig:"VALUES"`
	ValueFiles   []string `envconfig:"VALUE_FILES"`
}

const (
	gcloudBin  = "gcloud"
	gsutilBin  = "gsutil"
	kubectlBin = "kubectl"
	helmBin    = "helm"

	lintPkg   = "lint"
	createPkg = "create"
	pushPkg   = "push"
	pullPkg   = "pull"
	deployPkg = "deploy"
	testPkg   = "test"

	updateWaitTime = 10 * time.Second
	updateRetries  = 10
)

// Exec executes the plugin step.
func (p Plugin) Exec() error {
	// only setup project when needed args are provided
	if p.Project != "" && p.Cluster != "" && (p.Zone != "" || p.Region != "") {
		if err := setupProject(p.Project, p.Cluster, p.Zone, p.Region, p.Debug); err != nil {
			return err
		}
		if err := helmInit(p.Debug); err != nil {
			return err
		}
	}

	for _, a := range p.Actions {
		switch a {
		case lintPkg:
			if err := p.lintPackage(); err != nil {
				return err
			}
		case createPkg:
			if err := p.createPackage(); err != nil {
				return err
			}
		case pushPkg:
			if err := p.pushPackage(); err != nil {
				return err
			}
		case pullPkg:
			if err := p.pullPackage(); err != nil {
				return err
			}
		case deployPkg:
			if err := p.deployPackage(); err != nil {
				return err
			}
		case testPkg:
			if err := p.testPackage(); err != nil {
				return err
			}
		default:
			return errors.New("unknown action")
		}
	}

	return nil
}

// setupProject setups gcloud project.
func setupProject(project, cluster, zone, region string, debug bool) error {
	// project configuration
	cmd := exec.Command(gcloudBin, "config", "set", "project", project)
	if err := run(cmd, debug); err != nil {
		return fmt.Errorf("could not the configure the project with glcoud: %v", err)
	}

	// cluster configuration
	cmd = exec.Command(gcloudBin, "container", "clusters", "get-credentials", cluster, "--zone", zone)
	if region != "" {
		// override zone when region is set
		cmd = exec.Command(gcloudBin, "container", "clusters", "get-credentials", cluster, "--region", region)
	}

	if err := run(cmd, debug); err != nil {
		return fmt.Errorf("could not configure the cluster with glcoud: %v", err)
	}

	return nil
}

// setupAuth configures gcloud to use the given authFile
func setupAuth(authFile string, debug bool) error {
	if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", authFile); err != nil {
		return fmt.Errorf("could not set GOOGLE_APPLICATION_CREDENTIALS env variable: %v", err)
	}

	// authorization
	cmd := exec.Command(gcloudBin, "auth", "activate-service-account", fmt.Sprintf("--key-file=%s", authFile))
	if err := run(cmd, debug); err != nil {
		return fmt.Errorf("could not authorize with glcoud: %v", err)
	}
	return nil
}

// createPackage creates Helm package for Kubernetes.
// helm package --version $PLUGIN_CHART_VERSION $PLUGIN_CHART_PATH
func (p Plugin) createPackage() error {
	return run(exec.Command(helmBin, "package", "--version", p.ChartVersion, p.ChartPath), p.Debug)
}

// cpPackage copies a file from SOURCE to DEST
// gsutil cp SOURCE DEST
func (p Plugin) cpPackage(source string, dest string) error {
	return run(exec.Command(gsutilBin, "cp", source, dest), p.Debug)
}

// cpPackage pulls helm chart from Google Storage to local
// gsutil cp $PACKAGE-$PLUGIN_CHART_VERSION.tgz gs://$PLUGIN_BUCKET
func (p Plugin) pullPackage() error {
	return p.cpPackage(
		fmt.Sprintf("gs://%s/%s-%s.tgz", p.Bucket, p.Package, p.ChartVersion),
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
	)
}

// pushPackage pushes Helm package to the Google Storage.
// gsutil cp $PACKAGE-$PLUGIN_CHART_VERSION.tgz gs://$PLUGIN_BUCKET
func (p Plugin) pushPackage() error {
	return p.cpPackage(
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
		fmt.Sprintf("gs://%s", p.Bucket),
	)
}

// helm lint $CHARTPATH -i
func (p Plugin) lintPackage() error {
	return run(exec.Command(helmBin, "lint", p.ChartPath), p.Debug)
}

// helm upgrade $PACKAGE $PACKAGE-$PLUGIN_CHART_VERSION.tgz -i
func (p Plugin) deployPackage() error {
	args := []string{
		helmBin,
		"upgrade",
		p.Release,
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
	}
	if len(p.ValueFiles) > 0 {
		for _, f := range p.ValueFiles {
			args = append(args, "-f", f)
		}
	}
	if len(p.Values) > 0 {
		args = append(args, "--set", strings.Join(p.Values, ","))
	}
	if p.Recreate {
		args = append(args, "--recreate-pods")
	}
	args = append(args, "--install")
	args = append(args, "--namespace", p.Namespace)

	if p.Wait {
		args = append(args, "--wait", "--timeout", strconv.Itoa(int(p.WaitTimeout)))
	}
	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

// helm test $PACKAGE
func (p Plugin) testPackage() error {
	args := []string{helmBin, "test", p.Release, "--cleanup", "--timeout", strconv.Itoa(int(p.WaitTimeout))}
	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

type semVer struct {
	Version string `json:"version"`
}
type helmVersions struct {
	Client semVer `json:"client"`
	Server semVer `json:"server"`
}

// fetchHelmVersions queries the helm and tiller versions
func fetchHelmVersions(debug bool) (*helmVersions, error) {
	// prepare the template for the `helm version --template` cmd line call.
	template, err := json.Marshal(&helmVersions{
		Client: semVer{Version: "{{ .Client.SemVer }}"},
		Server: semVer{Version: "{{ .Server.SemVer }}"},
	})
	if err != nil {
		return nil, fmt.Errorf("could not marshal version template: %v", err)
	}

	cmd := exec.Command(helmBin, "version", "--template", string(template))
	if debug {
		log.Printf("running: %s", strings.Join(cmd.Args, " "))
	}
	out, err := cmd.CombinedOutput()
	if debug {
		log.Printf("%s", out)
	}
	if err != nil {
		return nil, fmt.Errorf("could not run command: %v", err)
	}

	var versions helmVersions
	if err := json.Unmarshal(out, &versions); err != nil {
		return nil, fmt.Errorf("could not parse client version: %v", err)
	}

	return &versions, nil
}

// helmInit inits Triller on Kubernetes cluster.
func helmInit(debug bool) error {
	ver, err := fetchHelmVersions(debug)
	if err != nil {
		return fmt.Errorf("could not fetch helm versions: %v", err)
	}

	clientVersion, err := version.NewVersion(ver.Client.Version)
	if err != nil {
		return fmt.Errorf("could not convert client version to semver: %v", err)
	}
	serverVersion, err := version.NewVersion(ver.Server.Version)
	if err != nil {
		return fmt.Errorf("could not convert server version to semver: %v", err)
	}

	var option string
	var wait bool
	switch clientVersion.Compare(serverVersion) {
	case -1: // client is older than tiller
		return fmt.Errorf("helm client is out of date")
	case 1: // client is newer than tiller
		option = "--upgrade"
		wait = true
	default: // client and tiller are at the same version
		option = "--client-only"
	}

	cmd := exec.Command(helmBin, "init", option)
	if err := run(cmd, debug); err != nil {
		return fmt.Errorf("could not run command '%s': %v", strings.Join(cmd.Args, " "), err)
	}

	if wait {
		var err error
		for i := 0; i < updateRetries; i++ {
			time.Sleep(updateWaitTime)
			err = run(exec.Command(helmBin, "version", "--server"), debug)
			if err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("could not wait for tiller: max retries exceeded: %v", err)
		}
	}

	return nil
}

func (p Plugin) movePkg() error {
	if err := os.Mkdir(p.Bucket, os.ModeDir); err != nil {
		return err
	}
	return cp(
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
		fmt.Sprintf("%s/%s-%s.tgz", p.Bucket, p.Package, p.ChartVersion),
	)
}

// cp copies file
func cp(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	// no need to check errors on read only file, we already got everything
	// we need from the filesystem, so nothing can go wrong now.
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// scanNamed maps named regex groups to a golang map
func scanNamed(str string, rg *regexp.Regexp) (map[string]string, error) {
	result := make(map[string]string)
	for _, match := range rg.FindAllStringSubmatch(str, -1) {
		for i, name := range rg.SubexpNames() {
			if i != 0 && match[i] != "" && name != "" {
				result[name] = match[i]
			}
		}
	}

	if len(result) == 0 {
		return nil, errors.New("emtpy resultset")
	}

	return result, nil
}

func run(cmd *exec.Cmd, debug bool) error {
	if debug {
		log.Printf("running: %s", strings.Join(cmd.Args, " "))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}
