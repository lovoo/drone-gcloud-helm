package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	gcloudBin  = "/opt/google-cloud-sdk/bin/gcloud"
	gsutilBin  = "/opt/google-cloud-sdk/bin/gsutil"
	kubectlBin = "/opt/google-cloud-sdk/bin/kubectl"
	helmBin    = "/opt/google-cloud-sdk/bin/helm"

	lintPkg   = "lint"
	createPkg = "create"
	pushPkg   = "push"
	pullPkg   = "pull"
	deployPkg = "deploy"
	testPkg   = "test"
)

var reVersions = regexp.MustCompile(`(?P<realm>Client|Server): &version.Version.SemVer:"(?P<semver>.*?)".*?GitCommit:"(?P<commit>.*?)".*?GitTreeState:"(?P<treestate>.*?)"`)

// Exec executes the plugin step.
func (p Plugin) Exec() error {
	// only setup project when needed args are provided
	if p.Project != "" && p.Cluster != "" && p.Zone != "" {
		if err := setupProject(p.Project, p.Cluster, p.Zone, p.Debug); err != nil {
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
func setupProject(project, cluster, zone string, debug bool) error {
	// project configuration
	cmd := exec.Command(gcloudBin, "config", "set", "project", project)
	if err := run(cmd, debug); err != nil {
		return fmt.Errorf("could not the configure the project with glcoud: %v", err)
	}

	// cluster configuration
	cmd = exec.Command(gcloudBin, "container", "clusters", "get-credentials", cluster, "--zone", zone)
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
	args := []string{helmBin, "test", p.Release}
	if p.Wait {
		args = append(args, "--wait", "--timeout", strconv.Itoa(int(p.WaitTimeout)))
	}
	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

// fetchHelmVersions returns helm and tiller versions as map
func fetchHelmVersions(debug bool) (map[string]map[string]string, error) {
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(helmBin, "version")
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if debug {
		log.Printf("running: %s", strings.Join(cmd.Args, " "))
	}
	if err := cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
	}
	if debug {
		log.Printf("%s", out.String())
	}

	lines := strings.Split(out.String(), "\n")
	versions := make(map[string]map[string]string)

	// we just care about the first two lines
	for _, line := range lines[:2] {
		entry, reErr := scanNamed(line, reVersions)
		if reErr != nil {
			return nil, reErr
		}
		versions[strings.ToLower(entry["realm"])] = entry
	}

	return versions, nil
}

// helmInit inits Triller on Kubernetes cluster.
func helmInit(debug bool) error {
	args := []string{"init"}

	ver, err := fetchHelmVersions(debug)
	if err == nil {
		switch strings.Compare(ver["client"]["semver"], ver["server"]["semver"]) {
		case -1: // client is older than tiller
			return fmt.Errorf("helm client is out of date")
		case 1: // client is newer than tiller
			args = append(args, "--upgrade")
		default: // client and tiller are at the same version
			args = append(args, "--client-only")
		}
	}

	if err := run(exec.Command(helmBin, args...), debug); err != nil {
		return err
	}

	// poll for tiller (call helm version 10 times)
	return pollTiller(10, debug)
}

// pollTiller repeatedly calls helm version and checks its exit code
func pollTiller(retries int, debug bool) error {
	var err error
	for i := 0; i < retries; i++ {
		if err = run(exec.Command(helmBin, "version"), debug); err == nil {
			return nil
		}
	}
	return err
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
