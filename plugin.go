package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	sops_decrypt "go.mozilla.org/sops/v3/decrypt"
)

// Plugin defines the Helm plugin parameters.
type Plugin struct {
	Debug          bool     `envconfig:"DEBUG"`
	ShowEnv        bool     `envconfig:"SHOW_ENV"`
	Wait           bool     `envconfig:"WAIT"`
	Recreate       bool     `envconfig:"RECREATE_PODS" default:"false"`
	WaitTimeout    uint32   `envconfig:"WAIT_TIMEOUT" default:"300"`
	Actions        []string `envconfig:"ACTIONS" required:"true"`
	AuthKey        string   `envconfig:"AUTH_KEY"`
	KeyPath        string   `envconfig:"KEY_PATH"`
	Zone           string   `envconfig:"ZONE"`
	Region         string   `envconfig:"REGION"`
	Cluster        string   `envconfig:"CLUSTER"`
	Project        string   `envconfig:"PROJECT"`
	Namespace      string   `envconfig:"NAMESPACE"`
	ChartRepo      string   `envconfig:"CHART_REPO"`
	Bucket         string   `envconfig:"BUCKET"`
	ChartPath      string   `envconfig:"CHART_PATH" required:"true"`
	ChartVersion   string   `envconfig:"CHART_VERSION"`
	Release        string   `envconfig:"RELEASE"`
	Package        string   `envconfig:"PACKAGE"`
	Values         []string `envconfig:"VALUES"`
	ValueFiles     []string `envconfig:"VALUE_FILES"`
	Secrets        []string `envconfig:"SECRETS"`
	HelmStableRepo string   `envconfig:"HELM_STABLE_REPO" default:"https://charts.helm.sh/stable"`
}

const (
	gcloudBin  = "gcloud"
	gsutilBin  = "gsutil"
	kubectlBin = "kubectl"
	helmBin    = "helm"

	lintPkg       = "lint"
	createPkg     = "create"
	pushPkg       = "push"
	pullPkg       = "pull"
	deployPkg     = "deploy"
	testPkg       = "test"
	dependencyPkg = "dep"

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
		case dependencyPkg:
			if err := p.addRepo(); err != nil {
				return err
			}
			if err := p.dependencyUpdate(); err != nil {
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
	args := []string{
		helmBin,
		"lint",
		p.ChartPath,
	}

	args = append(args, p.createValueFileArgs()...)

	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

func (p Plugin) dependencyUpdate() error {
	return run(exec.Command(helmBin, "dependency", "update", p.ChartPath), p.Debug)
}

func (p Plugin) createValueFileArgs() []string {
	var args []string
	if len(p.ValueFiles) > 0 {
		for _, f := range p.ValueFiles {
			args = append(args, "-f", f)
		}
	}
	if len(p.Values) > 0 {
		args = append(args, "--set", strings.Join(p.Values, ","))
	}
	return args
}

func (p Plugin) addRepo() error {
	if err := run(exec.Command(helmBin, "repo", "add", "stable", p.HelmStableRepo), p.Debug); err != nil {
		return fmt.Errorf("could not add stable repo '%s': %w", p.HelmStableRepo, err)
	}
	if err := run(exec.Command(helmBin, "repo", "update"), p.Debug); err != nil {
		return fmt.Errorf("could not update repos: %w", err)
	}
	return nil
}

// helm upgrade $PACKAGE $PACKAGE-$PLUGIN_CHART_VERSION.tgz -i
func (p Plugin) deployPackage() error {
	// We need to create the namespace because Helm 3 does not create the namespace for us anymore.
	if err := createNamespace(p.Namespace, p.Debug); err != nil {
		return fmt.Errorf("could not create namespace: %w", err)
	}

	args := []string{
		helmBin,
		"upgrade",
		p.Release,
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
	}

	args = append(args, p.createValueFileArgs()...)

	var tempFiles []string
	defer func() {
		for _, f := range tempFiles {
			if err := os.Remove(f); err != nil {
				fmt.Printf("could not remove temp file: %v", err)
			}
		}
	}()
	for _, f := range p.Secrets {
		cleartext, err := sops_decrypt.File(f, "yaml")
		if err != nil {
			return fmt.Errorf("could not decrypt secret file: %w", err)
		}
		tmp, err := ioutil.TempFile(".", "decrypted")
		if err != nil {
			return fmt.Errorf("could not create temp file for the decrypted secrets: %w", err)
		}
		defer tmp.Close()
		tempFiles = append(tempFiles, tmp.Name())

		if _, err := tmp.Write(cleartext); err != nil {
			return fmt.Errorf("could not write temp file with decrypted secrets: %w", err)
		}
		if err := tmp.Sync(); err != nil {
			return fmt.Errorf("could not sync temp file with decrypted secrets: %w", err)
		}
		args = append(args, "-f", tmp.Name())
	}

	if p.Recreate {
		args = append(args, "--recreate-pods")
	}
	args = append(args, "--install")
	args = append(args, "--namespace", p.Namespace)

	if p.Wait {
		args = append(args, "--wait", "--timeout", fmt.Sprintf("%ds", p.WaitTimeout))
	}
	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

// helm test $PACKAGE
func (p Plugin) testPackage() error {
	args := []string{
		helmBin, "test", p.Release,
		"--namespace", p.Namespace,
		"--timeout", fmt.Sprintf("%ds", p.WaitTimeout),
	}
	return run(exec.Command("/bin/sh", "-c", strings.Join(args, " ")), p.Debug)
}

type semVer struct {
	Version string `json:"version"`
}
type helmVersions struct {
	Client semVer `json:"client"`
	Server semVer `json:"server"`
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

func createNamespace(name string, debug bool) error {
	checkNS := exec.Command(kubectlBin, "get", "namespace", "--ignore-not-found", name)
	response, err := checkNS.CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not check if namespace exists: %w", err)
	}

	if len(response) == 0 {
		return run(exec.Command(kubectlBin, "create", "namespace", name), debug)
	}

	return nil
}
