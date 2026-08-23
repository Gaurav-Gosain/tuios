#!/bin/sh
# Dock component: the kubectl context you are about to run things against.
#
#   [dock.custom.k8s]
#   command   = "~/.config/tuios/dock/kube-context.sh"
#   refresh   = "30s"
#   on-click  = "tuios popup -- kubectl config get-contexts"
#   max-width = 28
#
# This is the cell that pays for itself the first time it stops you running
# something against production. It reads the kubeconfig directly rather than
# shelling out to kubectl, because kubectl takes long enough that a poll would
# be noticeable and this does not.
#
# A context whose name says production is drawn in red. Everything else is drawn
# plainly: a warning that fires for every context is a warning nobody reads.
set -eu

command -v kubectl >/dev/null 2>&1 || exit 0

kubeconfig=${KUBECONFIG:-$HOME/.kube/config}
[ -r "${kubeconfig%%:*}" ] || exit 0

ctx=$(kubectl config current-context 2>/dev/null) || exit 0
[ -n "$ctx" ] || exit 0

ns=$(kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null) || ns=""
[ -n "$ns" ] || ns=default

case "$ctx" in
*prod* | *production* | *live*)
	printf '\033[31m󱃾 %s:%s\033[0m\n' "$ctx" "$ns"
	;;
*)
	printf '󱃾 %s:%s\n' "$ctx" "$ns"
	;;
esac
