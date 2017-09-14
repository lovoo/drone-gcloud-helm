# drone-gcloud-helm

Drone 0.6 plugin to use create and deploy Helm charts for Kubernetes and push Helm package to Google Storage. You will need to generate a [JSON token](https://developers.google.com/console/help/new/#serviceaccounts) to authenticate to the Kuebernetes cluster and to push Helm package to the Google Storage.

The following parameters are used to configure this plugin:

* `debug` - enable debug mode.
* `show_env` - outputs a list of env vars without values.
* `wait` - Wait until all Pods, PVCs, Services, and min number of Pods of a Deployment are in a ready state before marking the release as successful.
* `wait_timeout` - Time in seconds to wait for any individual kubernetes operation (like Jobs for hooks) (default 300).
* `recreate-pods` - If true, uses helm upgrade with the `recreate-pods` flag.
* `actions` - list of actions over chart - `lint`, `create`, `push`, `deploy`. Required and order is important (except lint).
* `zone` - zone of the Kubernetes cluster.
* `cluster` - the Kubernetes cluster name.
* `project` - the Google project identifier.
* `namespace` - the Kubernetes namespace to install in.
* `bucket` - the Google Storage Bucket name to push Helm package into it.
* `chart_repo` - the Helm charts repository (defaul ig `https://$(BUCKET).storage.googleapis.com/`)
* `chart_path` - the path to the Helm chart (e.g. chart/foo).
* `chart_version` - the version of the chart.
* `package` - the package name. Default is chart name.
* `release` - the release name used for helm upgrade. Defaults to package name.
* `values` - list of chart values. Would be set via `--set` Helm flag.

Auth Key Management:

Add a new secret, containing your JSON token to your project

```
drone secret add --image=gcr.io/lovoo-ci/drone-gcloud-helm:1.1.0 --name=AWESOME_GCLOUD_TOKEN --value=@/path/to/your/token.json octocat/helloworld
```

configure the drone-gcloud-helm plugin in your .drone.yaml to use your secret and alias it to AUTH_KEY

```
  secrets:
    - source: AWESOME_GCLOUD_TOKEN
      target: AUTH_KEY
```


Sample configuration:

```
deploy:
  image: foobar/drone-gcloud-helm
  debug: true
  actions:
    - lint
    - create
    - push
    - deploy
  chart_path: chart/foo
  chart_version: ${DRONE_BUILD_NUMBER}
  project: foo-project
  cluster: foo-cluster-1
  zone: europe-west1-b
  bucket: foo-charts
  secrets:
    - source: AWESOME_GCLOUD_TOKEN
      target: AUTH_KEY
  values:
    - "docker.tag=${DRONE_BUILD_NUMBER}"
  when:
    branch: master
    event: push
```

Sample configuration for linting only:

```
lint:
  image: foobar/drone-gcloud-helm
  actions:
    - lint
  chart_path: chart/foo
  when:
    branch: master
    event: push
```
