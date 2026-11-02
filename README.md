# storagecheck

![Coverage](https://img.shields.io/badge/Coverage-0-red)

checks in Kubernetes cluster the possibilty to create a PVC and bound on a POD, periodically.

serve Prometheus metrics for the status

<img src="storagecheck-1.png" alt="storagecheck-1" width="680"/>
<img src="storagecheck-2.png" alt="storagecheck-2" width="680"/>

## installation

via Helm

## alert

Runbook for `StorageCheckFailed`. The counter for failed checks is bigger then 0.

* Check Pod/PVC for `Pending` state
* Describe resource to find out the reason
* Find outz which `StorageClass` is used
* Repair CSI of the corresponding StorageClass
* Restart storagecheck deployment to reset counter

## metrics

```
# HELP storage_check_cleanup_failure_total Total number of failed cleanups of previous checks
# TYPE storage_check_cleanup_failure_total counter
storage_check_cleanup_failure_total 0
# HELP storage_check_cleanup_success_total Total number of successful cleanups of previous checks
# TYPE storage_check_cleanup_success_total counter
storage_check_cleanup_success_total 0
# HELP storage_check_duration_seconds Duration of storage checks in seconds
# TYPE storage_check_duration_seconds histogram
storage_check_duration_seconds_bucket{le="0.005"} 0
storage_check_duration_seconds_bucket{le="0.01"} 0
storage_check_duration_seconds_bucket{le="0.025"} 0
storage_check_duration_seconds_bucket{le="0.05"} 0
storage_check_duration_seconds_bucket{le="0.1"} 0
storage_check_duration_seconds_bucket{le="0.25"} 0
storage_check_duration_seconds_bucket{le="0.5"} 0
storage_check_duration_seconds_bucket{le="1"} 0
storage_check_duration_seconds_bucket{le="2.5"} 0
storage_check_duration_seconds_bucket{le="5"} 0
storage_check_duration_seconds_bucket{le="10"} 1
storage_check_duration_seconds_bucket{le="+Inf"} 1
storage_check_duration_seconds_sum 8.02171965
storage_check_duration_seconds_count 1
# HELP storage_check_failure_total Total number of failed storage checks
# TYPE storage_check_failure_total counter
storage_check_failure_total 0
# HELP storage_check_success_total Total number of successful storage checks
# TYPE storage_check_success_total counter
storage_check_success_total 1
```

## Dependency updates

Renovate uses the [shared update policy](https://github.com/eumel8/renovate-config).
Regular updates are proposed on Monday mornings (Europe/Berlin), with at most
three open regular update PRs. Go module updates are grouped separately from Go
toolchain updates; Dockerfile, `go.mod` and release workflow Go versions share
the toolchain group. Major upgrades require approval in the Dependency Dashboard.
Auto-merge is disabled.

The Renovate GitHub App must have access to this repository. Merge the shared
preset first, then this repository's onboarding PR. Dependabot version updates
are replaced by Renovate; GitHub vulnerability alerts and Dependabot security
updates are separate settings.

## Credits

Frank Kloeker f.kloeker@telekom.de

Life is for sharing. If you have an issue with the code or want to improve it, feel free to open an issue or an pull request.
