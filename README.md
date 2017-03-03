# drone-gcloud-helm

Drone 0.5 plugin to use create and deploy Helm charts for Kubernetes and push Helm package to Google Storage. You will need to generate a [JSON token](https://developers.google.com/console/help/new/#serviceaccounts) to authenticate to the Kuebernetes cluster and to push Helm package to the Google Storage.

The following parameters are used to configure this plugin:

* `debug` - enable debug mode.
* `actions` - list of actions over chart - `create`, `push`, `deploy`. Required.
* `auth_key` - json authentication key for service account.
* `zone` - zone of the Kubernetes cluster.
* `cluster` - the Kubernetes cluster name.
* `project` - the Google project identifier.
* `bucket` - the Google Storage Bucket name to push Helm package into it.
* `chart_path` - the path to the Helm chart (e.g. chart/foo).
* `chart_version` - the version of the chart.
* `package` - the package name. Default is chart name.
* `values` - list of chart values. Would be set via `--set` Helm flag.

Sample configuration:

```
deploy:
  image: foobar/drone-gcloud-helm
  debug: true
  actions:
    - create
    - push
    - deploy
  chart_path: chart/foo
  chart_version: ${DRONE_BUILD_NUMBER}
  project: foo-project
  cluster: foo-cluster-1
  zone: europe-west1-b
  bucket: foo-charts
  auth_key: >
    {
      "private_key_id": "...",
      "private_key": "...",
      "client_email": "...",
      "client_id": "...",
      "type": "..."
    }
  values:
    - "docker.tag=${DRONE_BUILD_NUMBER}"
  when:
    branch: master
    event: push
```
