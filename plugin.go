package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
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

// Exec executes the plugin step.
func (p Plugin) Exec() error {

	// only setup project when needed args are provided
	if p.Project != "" && p.Cluster != "" && p.AuthKey != "" {
		if err := p.setupProject(); err != nil {
			return err
		}

		if err := p.helmInit(); err != nil {
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
	cmd := exec.Command(helmBin, "package",
		"--version",
		p.ChartVersion,
		p.ChartPath,
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// cpPackage copies a file from SOURCE to DEST
// gsutil cp SOURCE DEST
func (p Plugin) cpPackage(source string, dest string) error {
	cmd := exec.Command(gsutilBin, "cp", source, dest)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
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
	helmcmd := fmt.Sprintf("%s lint %s",
		helmBin,
		p.ChartPath,
	)

	cmd := exec.Command("/bin/sh", "-c", helmcmd)
	cmd.Env = os.Environ()
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()

}

// helm upgrade $PACKAGE $PACKAGE-$PLUGIN_CHART_VERSION.tgz -i
func (p Plugin) deployPackage() error {
	if p.Debug {
		if err := p.kubeConfig(); err != nil {
			return err
		}
	}

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

	cmd := exec.Command("/bin/sh", "-c", helmcmd)
	cmd.Env = os.Environ()
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// setupProject setups gcloud project.
// gcloud auth activate-service-account --key-file=$KEY_FILE_PATH
// gcloud config set project $PLUGIN_PROJECT
// gcloud container clusters get-credentials $PLUGIN_CLUSTER --zone $PLUGIN_ZONE
func (p Plugin) setupProject() error {
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

	cmds := make([]*exec.Cmd, 0, 3)

	// authorization
	cmds = append(cmds, exec.Command(gcloudBin, "auth",
		"activate-service-account",
		fmt.Sprintf("--key-file=%s", tmpfile.Name()),
	))
	// project configuration
	cmds = append(cmds, exec.Command(gcloudBin, "config",
		"set",
		"project",
		p.Project,
	))
	// cluster configuration
	cmds = append(cmds, exec.Command(gcloudBin, "container",
		"clusters",
		"get-credentials",
		p.Cluster,
		"--zone",
		p.Zone,
	))

	for _, cmd := range cmds {
		if p.Debug {
			trace(cmd)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return err
		}
	}

	return os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tmpfile.Name())
}

// fetchHelmVersions returns helm and tiller versions as map
// helm version
func (p Plugin) fetchHelmVersions() (map[string]map[string]string, error) {
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(helmBin, "version")
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
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

// pollTiller repeatedly calls helm version and checks its exit code
// helm version
func (p Plugin) pollTiller(retryCount int) error {
	var pollErr error
	for ; retryCount >= 0; retryCount-- {
		pollCmd := exec.Command(helmBin, "version")
		if p.Debug {
			trace(pollCmd)
			pollCmd.Stdout = os.Stdout
			pollCmd.Stderr = os.Stderr
		}
		pollErr = pollCmd.Run()
		if pollErr == nil {
			break
		}
	}

	return pollErr
}

// helmInit inits Triller on Kubernetes cluster.
// helm init
func (p Plugin) helmInit() error {
	ver, err := p.fetchHelmVersions()
	var cmd *exec.Cmd

	if err != nil {
		// assume that Tiller is not installed
		// other errors will be fetched by helm init
		cmd = exec.Command(helmBin, "init")
	} else {
		switch strings.Compare(ver["client"]["semver"], ver["server"]["semver"]) {
		case -1: // client is older than tiller
			return errors.New("helm client is out of date")
		case 1: // client is newer than tiller
			cmd = exec.Command(helmBin, "init", "--upgrade")
			break
		default: // client and tiller are at the same version
			cmd = exec.Command(helmBin, "init", "--client-only")
			break
		}
	}

	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return err
	}

	// poll for tiller (call helm version 10 times)
	if err := p.pollTiller(10); err != nil {
		return err
	}

	return nil
}

func (p Plugin) addRepo() error {
	cmd := exec.Command(helmBin,
		"repo", "add",
		p.Bucket, p.ChartRepo,
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (p Plugin) updateRepo() error {
	cmd := exec.Command(helmBin,
		"repo", "update",
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (p Plugin) indexRepo() error {
	cmd := exec.Command(helmBin, "repo",
		"index", p.Bucket,
		"--url", p.ChartRepo,
	)
	if p.Debug {
		trace(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (p Plugin) movePkg() error {
	if err := os.Mkdir(p.Bucket, os.ModeDir); err != nil {
		return err
	}
	if err := cp(
		fmt.Sprintf("%s-%s.tgz", p.Package, p.ChartVersion),
		fmt.Sprintf("%s/%s-%s.tgz", p.Bucket, p.Package, p.ChartVersion),
	); err != nil {
		return err
	}
	return nil
}

func (p Plugin) kubeConfig() error {
	cmd := exec.Command(kubectlBin, "config", "view")
	trace(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// trace writes each command to stdout with the command wrapped in an xml
// tag so that it can be extracted and displayed in the logs.
func trace(cmd *exec.Cmd) {
	logrus.WithField("cmd", cmd.Args).Debug("debug")
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
