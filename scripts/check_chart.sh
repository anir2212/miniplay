#!/bin/sh
# Lints and renders the Helm chart and checks the rendered text. This needs no
# cluster and proves nothing about a deployment. A kubeconform path is optional.
# Usage: scripts/check_chart.sh HELM [KUBECONFORM]
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
HELM=${1:?path to helm}
KUBECONFORM=${2:-}
CHART=$ROOT/deploy/helm/miniplay
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

"$HELM" lint "$CHART" -f "$CHART/values-kind.yaml" | tail -1
"$HELM" template mp "$CHART" -f "$CHART/values-kind.yaml" >"$WORK/kind.yaml"

# Unset required values must stop rendering, with a message naming the value.
if "$HELM" template mp "$CHART" >"$WORK/out" 2>&1; then fail "rendered with no Secret name"; fi
grep -q "Set existingSecret" "$WORK/out" || fail "missing-Secret message: $(cat "$WORK/out")"
if "$HELM" template mp "$CHART" --set existingSecret=x >"$WORK/out" 2>&1; then fail "rendered with no Kafka brokers"; fi
grep -q "Set kafka.brokers" "$WORK/out" || fail "missing-brokers message: $(cat "$WORK/out")"

# The variants render too.
"$HELM" template mp "$CHART" -f "$CHART/values-kind.yaml" --set gateway.tlsSecret=gateway-tls >"$WORK/tls.yaml"
grep -q "secretName: gateway-tls" "$WORK/tls.yaml" || fail "TLS Secret not mounted"
grep -q "scheme: HTTPS" "$WORK/tls.yaml" || fail "TLS probes still use HTTP"
"$HELM" template mp "$CHART" -f "$CHART/values-kind.yaml" --set score.autoscaling.enabled=false --set migrations.enabled=false >"$WORK/plain.yaml"
if grep -q "kind: HorizontalPodAutoscaler" "$WORK/plain.yaml"; then fail "autoscaler rendered while disabled"; fi
if grep -q "kind: Job" "$WORK/plain.yaml"; then fail "migration Job rendered while disabled"; fi

python3 -I - "$WORK/kind.yaml" "$WORK/plain.yaml" "$ROOT" <<'PYTHON'
import pathlib, re, sys
kind, plain = (pathlib.Path(p).read_text() for p in sys.argv[1:3])
root = pathlib.Path(sys.argv[3])

def documents(text):
    return [d for d in re.split(r"^---$", text, flags=re.M) if d.strip()]

def find(text, kind_name, name):
    for doc in documents(text):
        if re.search(rf"^kind: {kind_name}$", doc, re.M) and re.search(rf"^  name: {name}$", doc, re.M):
            return doc
    raise SystemExit(f"FAIL: no {kind_name} {name}")

# Every long-running command has a Deployment, with each variable its main.go requires.
# Secret-bearing values come by reference; the loop further down refuses literal ones.
need = {
    "gateway": ["SESSION_URL", "SCORE_URL", "LEADERBOARD_URL", "ADMIN_URL", "WEB_DIST", "MINIPLAY_HTTP_ADDR"],
    "session-svc": ["MINIPLAY_HMAC_SECRET", "REDIS_URL", "MINIPLAY_HTTP_ADDR", "MINIPLAY_TRUSTED_PROXIES"],
    "score-svc": ["MINIPLAY_HMAC_SECRET", "DATABASE_URL", "REDIS_URL", "MINIPLAY_HTTP_ADDR", "MINIPLAY_TRUSTED_PROXIES", "MINIPLAY_FLAG_TTL"],
    "leaderboard-svc": ["DATABASE_URL", "REDIS_URL", "MINIPLAY_HTTP_ADDR"],
    "admin-svc": ["DATABASE_URL", "MINIPLAY_ADMIN_TOKEN", "MINIPLAY_HTTP_ADDR", "MINIPLAY_TRUSTED_PROXIES", "MINIPLAY_FLAG_TTL", "MINIPLAY_ADMIN_ACTOR"],
    "outbox-publisher": ["DATABASE_URL", "KAFKA_BROKERS"],
    "stats-worker": ["DATABASE_URL", "REDIS_URL", "KAFKA_BROKERS"],
}
for service, variables in need.items():
    if not (root / "cmd" / service / "main.go").exists():
        raise SystemExit(f"FAIL: chart names {service}, which is not a command")
    doc = find(kind, "Deployment", f"mp-{service}")
    for variable in variables:
        if f"- name: {variable}\n" not in doc:
            raise SystemExit(f"FAIL: {service} lacks {variable}")
    for pattern in ("runAsNonRoot: true", "allowPrivilegeEscalation: false", "readOnlyRootFilesystem: true", "cpu:"):
        if pattern not in doc:
            raise SystemExit(f"FAIL: {service} lacks {pattern}")

# Secret-bearing variables must come from a secretKeyRef, never a literal value.
for variable in ("DATABASE_URL", "REDIS_URL", "MINIPLAY_HMAC_SECRET", "MINIPLAY_ADMIN_TOKEN"):
    for match in re.finditer(rf"- name: {variable}\n\s+(\S+)", kind):
        if match.group(1) != "valueFrom:":
            raise SystemExit(f"FAIL: {variable} is a literal value")
if re.search(r"^kind: Secret$", kind, re.M):
    raise SystemExit("FAIL: the chart must not create a Secret")
if re.search(r"(BEGIN [A-Z ]*PRIVATE KEY|postgres://|redis://)", kind):
    raise SystemExit("FAIL: rendered output contains a credential or connection URL")

# The autoscaler scales score-svc by CPU, the Deployment leaves replicas to it, and it
# has the CPU request that utilisation is measured against.
hpa = find(kind, "HorizontalPodAutoscaler", "mp-score-svc")
for pattern in (r"kind: Deployment\n\s+name: mp-score-svc", r"name: cpu", r"type: Utilization", r"averageUtilization: 70", r"minReplicas: 1", r"maxReplicas: 4"):
    if not re.search(pattern, hpa):
        raise SystemExit(f"FAIL: HPA lacks {pattern}")
score = find(kind, "Deployment", "mp-score-svc")
if re.search(r"^  replicas:", score, re.M) or not re.search(r"requests:\n\s+cpu: 200m", score):
    raise SystemExit("FAIL: score-svc replicas or CPU request")
if not re.search(r"^  replicas: 1$", find(plain, "Deployment", "mp-score-svc"), re.M):
    raise SystemExit("FAIL: score-svc has no replica count with the autoscaler off")

job = find(kind, "Job", "mp-migrate")
if "helm.sh/hook: pre-install,pre-upgrade" not in job or "MIGRATION_DIR" not in job:
    raise SystemExit("FAIL: migration Job")

kinds = sorted(re.findall(r"^kind: (\w+)$", kind, re.M))
print("rendered:", {k: kinds.count(k) for k in sorted(set(kinds))})
PYTHON

if [ -n "$KUBECONFORM" ]; then
  mkdir -p "$ROOT/.cache/kubeconform"
  "$KUBECONFORM" -strict -summary -kubernetes-version 1.33.0 -cache "$ROOT/.cache/kubeconform" "$WORK/kind.yaml"
  "$KUBECONFORM" -strict -summary -kubernetes-version 1.33.0 -cache "$ROOT/.cache/kubeconform" "$WORK/tls.yaml"
else
  echo "kubeconform not given: rendered output was not checked against Kubernetes schemas"
fi
echo "chart checks passed"
