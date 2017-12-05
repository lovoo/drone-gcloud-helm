package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/kelseyhightower/envconfig"
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
)

var reVersions = regexp.MustCompile(`(?P<realm>Client|Server): &version.Version.SemVer:"(?P<semver>.*?)".*?GitCommit:"(?P<commit>.*?)".*?GitTreeState:"(?P<treestate>.*?)"`)

func newPlugin() (*Plugin, error) {
	var p Plugin
	if err := envconfig.Process("plugin", &p); err != nil {
		return nil, fmt.Errorf("failed to parse parameters: %v", err)
	}
	if p.ShowEnv {
		for _, e := range os.Environ() {
			pair := strings.Split(e, "=")
			fmt.Println(pair[0])
		}
	}
	if p.Package == "" {
		s := strings.Split(p.ChartPath, "/")
		p.Package = s[len(s)-1]
	}
	if p.Release == "" {
		p.Release = p.Package
	}
	if p.ChartRepo == "" && p.Bucket != "" {
		p.ChartRepo = fmt.Sprintf("https://%s.storage.googleapis.com/", p.Bucket)
	}
	if p.Namespace == "" {
		p.Namespace = "default"
	}
	return nil, nil
}

// Exec executes the plugin step.
func (p Plugin) Exec() error {

	// only setup project when needed args are provided
	if p.Project != "" && p.Cluster != "" && (p.AuthKey != "" || p.KeyPath != "") {
		if err := p.setupProject(); err != nil {
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
		default:
			return errors.New("unknown action")
		}
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
	tarFile := fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion)
	object := fmt.Sprintf("gs://%s/%s", p.Bucket, tarFile)
	return p.cpPackage(object, tarFile)
}

// pushPackage pushes Helm package to the Google Storage.
// gsutil cp $PACKAGE-$PLUGIN_CHART_VERSION.tgz gs://$PLUGIN_BUCKET
func (p Plugin) pushPackage() error {
	tarFile := fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion)
	bucket := fmt.Sprintf("gs://%s", p.Bucket)
	return p.cpPackage(tarFile, bucket)
}

// helm lint $CHARTPATH -i
func (p Plugin) lintPackage() error {
	return run(exec.Command(helmBin, "lint", p.ChartPath), p.Debug)
}

// helm upgrade $PACKAGE $PACKAGE-$PLUGIN_CHART_VERSION.tgz -i
func (p Plugin) deployPackage() error {
	p.Values = append(p.Values, fmt.Sprintf("namespace=%s", p.Namespace))
	doRecreate := ""
	if p.Recreate {
		doRecreate = "--recreate-pods"
	}

	helmcmd := fmt.Sprintf("%s upgrade %s %s-%s.tgz --set %s %s --install --namespace %s",
		helmBin,
		p.Release,
		p.Package,
		p.ChartVersion,
		strings.Join(p.Values, ","),
		doRecreate,
		p.Namespace,
	)

	if p.Wait {
		helmcmd = fmt.Sprintf("%s --wait --timeout %d", helmcmd, p.WaitTimeout)
	}

	return run(exec.Command("/bin/sh", "-c", helmcmd), p.Debug)
}

// setupProject setups gcloud project.
// gcloud auth activate-service-account --key-file=$KEY_FILE_PATH
// gcloud config set project $PLUGIN_PROJECT
// gcloud container clusters get-credentials $PLUGIN_CLUSTER --zone $PLUGIN_ZONE
func (p Plugin) setupProject() error {
	authFile := p.KeyPath
	if p.AuthKey != "" && p.KeyPath == "" {
		tmpfile, err := ioutil.TempFile("", "auth-key.json")
		if err != nil {
			return err
		}

		if _, err := tmpfile.Write([]byte(p.AuthKey)); err != nil {
			return err
		}
		if err := tmpfile.Close(); err != nil {
			return err
		}
		authFile = tmpfile.Name()
	}
	if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", authFile); err != nil {
		return fmt.Errorf("could not set GOOGLE_APPLICATION_CREDENTIALS env variable: %v", err)
	}

	// authorization
	cmd := exec.Command(gcloudBin, "auth", "activate-service-account", fmt.Sprintf("--key-file=%s", authFile))
	if err := run(cmd, p.Debug); err != nil {
		return fmt.Errorf("could not authorize with glcoud: %v", err)
	}

	// project configuration
	cmd = exec.Command(gcloudBin, "config", "set", "project", p.Project)
	if err := run(cmd, p.Debug); err != nil {
		return fmt.Errorf("could not the configure the project with glcoud: %v", err)
	}

	// cluster configuration
	cmd = exec.Command(gcloudBin, "container", "clusters", "get-credentials", p.Cluster, "--zone", p.Zone)
	if err := run(cmd, p.Debug); err != nil {
		return fmt.Errorf("could not configure the cluster with glcoud: %v", err)
	}

	return nil
}

// fetchHelmVersions returns helm and tiller versions as a map
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
		return nil, fmt.Errorf("could not get helm version: %s", stderr.String())
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
	helmInit := fmt.Sprintf("%s init", helmBin)

	// in case of an error we will init tiller below
	ver, err := fetchHelmVersions(debug)
	if err == nil {
		switch strings.Compare(ver["client"]["semver"], ver["server"]["semver"]) {
		case -1: // client is older than tiller
			return fmt.Errorf("helm client is out of date")
		case 1: // client is newer than tiller
			helmInit += "--upgrade"
		default: // client and tiller are at the same version
			helmInit += "--client-only"
		}
	}

	if err := run(exec.Command(helmInit), debug); err != nil {
		return err
	}

	// poll for tiller (call helm version 10 times)
	return pollTiller(10, debug)
}

// pollTiller repeatedly calls helm version and checks its exit code
func pollTiller(retries int, debug bool) error {
	var err error
	for i := 0; i <= retries; i++ {
		if err = run(exec.Command(helmBin, "version"), debug); err == nil {
			return nil
		}
	}
	return err
}

func run(cmd *exec.Cmd, debug bool) error {
	if debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		log.Printf("running: %s", strings.Join(cmd.Args, " "))
	}
	return cmd.Run()
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
