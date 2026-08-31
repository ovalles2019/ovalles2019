# Security policy for rendered manifests.
#
# These run in CI against `helm template` output, so a chart that loses a
# hardening control fails the pipeline rather than being noticed during a
# cluster audit months later. Every rule states the consequence of violating it,
# because a policy failure with no explanation just gets suppressed.

package main

import rego.v1

# --- Workload identification -------------------------------------------------

workload_kinds := {"Deployment", "StatefulSet", "DaemonSet", "Job"}

is_workload if input.kind in workload_kinds

pod_spec := input.spec.template.spec if is_workload

containers contains c if {
	is_workload
	some c in pod_spec.containers
}

containers contains c if {
	is_workload
	some c in pod_spec.initContainers
}

name := sprintf("%s/%s", [input.kind, input.metadata.name])

# --- Container hardening -----------------------------------------------------

deny contains msg if {
	some c in containers
	not c.securityContext.runAsNonRoot
	msg := sprintf(
		"%s: container %q must set securityContext.runAsNonRoot. A container running as uid 0 that escapes its namespace is root on the node.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	c.securityContext.allowPrivilegeEscalation != false
	msg := sprintf(
		"%s: container %q must set allowPrivilegeEscalation=false, or a setuid binary inside it can regain privileges the container dropped.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	c.securityContext.privileged == true
	msg := sprintf(
		"%s: container %q is privileged, which disables essentially every container boundary.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	c.securityContext.readOnlyRootFilesystem != true
	msg := sprintf(
		"%s: container %q must set readOnlyRootFilesystem=true. A writable root filesystem lets an attacker drop a payload and persist it across the process restart.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	not "ALL" in object.get(c, ["securityContext", "capabilities", "drop"], [])
	msg := sprintf(
		"%s: container %q must drop ALL capabilities and add back only what it needs. The container runtime's default set includes CAP_NET_RAW and CAP_CHOWN, which nothing here uses.",
		[name, c.name],
	)
}

deny contains msg if {
	is_workload
	pod_spec.securityContext.seccompProfile.type != "RuntimeDefault"
	msg := sprintf(
		"%s: pod must set seccompProfile.type=RuntimeDefault. It blocks the ~300 syscalls a normal workload never issues and is the cheapest kernel attack-surface reduction available.",
		[name],
	)
}

deny contains msg if {
	is_workload
	pod_spec.hostNetwork == true
	msg := sprintf("%s: hostNetwork bypasses NetworkPolicy entirely.", [name])
}

deny contains msg if {
	is_workload
	pod_spec.hostPID == true
	msg := sprintf("%s: hostPID exposes every process on the node to this container.", [name])
}

deny contains msg if {
	is_workload
	some v in object.get(pod_spec, "volumes", [])
	v.hostPath
	msg := sprintf(
		"%s: volume %q uses hostPath, which mounts node state into the pod and is a standard container-escape path.",
		[name, v.name],
	)
}

# --- Images ------------------------------------------------------------------

deny contains msg if {
	some c in containers
	endswith(c.image, ":latest")
	msg := sprintf(
		"%s: container %q uses the :latest tag. Nobody can then say which commit is running, and two pods from the same release can be different code.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	not contains(c.image, ":")
	not contains(c.image, "@")
	msg := sprintf("%s: container %q has no image tag or digest, which resolves to :latest.", [name, c.name])
}

# --- Resources ---------------------------------------------------------------

deny contains msg if {
	some c in containers
	not c.resources.requests.cpu
	msg := sprintf(
		"%s: container %q must set resources.requests.cpu. The scheduler packs against the request, and HPA utilisation is a percentage of it, so an unset request makes both meaningless.",
		[name, c.name],
	)
}

deny contains msg if {
	some c in containers
	not c.resources.requests.memory
	msg := sprintf("%s: container %q must set resources.requests.memory.", [name, c.name])
}

# Memory is not compressible: a container over its limit is OOMKilled, but one
# with no limit at all takes the node and every pod on it.
deny contains msg if {
	some c in containers
	not c.resources.limits.memory
	msg := sprintf(
		"%s: container %q must set resources.limits.memory. Memory cannot be reclaimed under pressure, so one leaking pod without a limit takes down the whole node.",
		[name, c.name],
	)
}

# --- Probes ------------------------------------------------------------------

deny contains msg if {
	input.kind in {"Deployment", "StatefulSet"}
	some c in input.spec.template.spec.containers
	not c.readinessProbe
	msg := sprintf(
		"%s: container %q has no readinessProbe, so traffic is routed to it the moment the process starts and again while it is shutting down.",
		[name, c.name],
	)
}

deny contains msg if {
	input.kind in {"Deployment", "StatefulSet"}
	some c in input.spec.template.spec.containers
	not c.livenessProbe
	msg := sprintf("%s: container %q has no livenessProbe, so a wedged process is never restarted.", [name, c.name])
}

# A liveness probe that checks a dependency turns a downstream blip into a
# cluster-wide restart storm: every pod fails liveness at once and is killed
# while the dependency is already struggling.
deny contains msg if {
	input.kind in {"Deployment", "StatefulSet"}
	some c in input.spec.template.spec.containers
	c.livenessProbe.httpGet.path == c.readinessProbe.httpGet.path
	msg := sprintf(
		"%s: container %q points liveness and readiness at the same path %q. They answer different questions: liveness must not check dependencies or a database blip restarts every pod at once.",
		[name, c.name, c.livenessProbe.httpGet.path],
	)
}

# --- Availability ------------------------------------------------------------

deny contains msg if {
	input.kind == "Deployment"
	not input.spec.template.spec.topologySpreadConstraints
	msg := sprintf(
		"%s: no topologySpreadConstraints, so the scheduler may place every replica in one zone and a single zone failure takes the service down regardless of replica count.",
		[name],
	)
}

deny contains msg if {
	input.kind == "Deployment"
	input.spec.replicas
	input.spec.strategy.rollingUpdate.maxUnavailable != 0
	msg := sprintf(
		"%s: rollingUpdate.maxUnavailable must be 0 so capacity never drops below the desired count mid-rollout.",
		[name],
	)
}

# --- Service accounts --------------------------------------------------------

deny contains msg if {
	is_workload
	pod_spec.automountServiceAccountToken == true
	msg := sprintf(
		"%s: automountServiceAccountToken is on. Only a workload that calls the Kubernetes API needs a token; mounting it otherwise hands a compromised container a cluster credential.",
		[name],
	)
}

deny contains msg if {
	is_workload
	pod_spec.serviceAccountName == "default"
	msg := sprintf(
		"%s: uses the default ServiceAccount. A dedicated one per service is what makes IRSA and any future RBAC scoping possible.",
		[name],
	)
}

# --- Secrets -----------------------------------------------------------------

# Literal secret values must never reach a manifest, because manifests are
# committed, rendered into CI logs and stored in Helm release history.
deny contains msg if {
	input.kind == "Secret"
	input.data
	msg := sprintf(
		"%s: a Secret with literal data was rendered. Secrets belong in AWS Secrets Manager, synced by External Secrets, never in a chart.",
		[name],
	)
}

deny contains msg if {
	some c in containers
	some e in object.get(c, "env", [])
	e.value
	regex.match(`(?i)(password|secret|token|api[_-]?key|credential)`, e.name)
	msg := sprintf(
		"%s: container %q sets %q to a literal value. Use envFromSecret so the value comes from a Secret rather than the chart.",
		[name, c.name, e.name],
	)
}

# --- Service exposure --------------------------------------------------------

# One public entry point, an Ingress in front of the gateway. A LoadBalancer per
# workload bills for an ELB each and puts internal services on the internet.
deny contains msg if {
	input.kind == "Service"
	input.spec.type == "LoadBalancer"
	msg := sprintf(
		"%s: Service type LoadBalancer provisions a public ELB per service. Route through the shared Ingress instead.",
		[name],
	)
}

deny contains msg if {
	input.kind == "Service"
	input.spec.type == "NodePort"
	msg := sprintf("%s: NodePort exposes a port on every node and bypasses Ingress controls.", [name])
}

# --- Autoscaling -------------------------------------------------------------

deny contains msg if {
	input.kind == "HorizontalPodAutoscaler"
	input.apiVersion == "autoscaling/v1"
	msg := sprintf(
		"%s: autoscaling/v1 supports one CPU target and no scaling behaviour. Use autoscaling/v2 so scale-up and scale-down can be tuned independently.",
		[name],
	)
}

deny contains msg if {
	input.kind == "HorizontalPodAutoscaler"
	not input.spec.behavior
	msg := sprintf(
		"%s: HPA has no behavior block, so it uses controller defaults and will visibly flap on bursty load.",
		[name],
	)
}

# The controller takes the highest recommendation across this window; too short
# and a brief dip removes capacity a spike seconds later needs back.
deny contains msg if {
	input.kind == "HorizontalPodAutoscaler"
	input.spec.behavior.scaleDown.stabilizationWindowSeconds < 60
	msg := sprintf(
		"%s: scaleDown.stabilizationWindowSeconds is %d. Under 60s the HPA reclaims pods after every short lull and flaps.",
		[name, input.spec.behavior.scaleDown.stabilizationWindowSeconds],
	)
}

# A Deployment with a fixed replica count and an HPA fight each other: every
# helm upgrade resets the count and the HPA scales it back.
deny contains msg if {
	input.kind == "Deployment"
	input.spec.replicas
	input.metadata.annotations["platform.fleet.io/autoscaled"] == "true"
	msg := sprintf(
		"%s: sets spec.replicas while an HPA manages it. Omit replicas so a deploy during peak load does not briefly drop capacity.",
		[name],
	)
}
