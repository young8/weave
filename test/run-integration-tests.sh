#!/bin/bash
#
# Description:
#   This script runs all Weave Net's integration tests on the specified
#   provider (default: Google Cloud Platform).
#
# Usage:
#
#   Run all integration tests on Google Cloud Platform:
#   $ ./run-integration-tests.sh
#
#   Run all integration tests on Amazon Web Services:
#   PROVIDER=aws ./run-integration-tests.sh
#

DIR="$(dirname "$0")"
. "$DIR/../tools/provisioning/config.sh" # Import set_up_for_gcp, set_up_for_do and set_up_for_aws.
. "$DIR/config.sh" # Import greenly.

# Variables:
APP="weave-net"
# Only used when PROVIDER is gcp:
PROJECT="weave-net-tests"
if [ -n "$CIRCLECI" ]; then
  SUFFIX="${CIRCLE_BUILD_NUM}-$CIRCLE_NODE_INDEX"
else
  SUFFIX=${SUFFIX:-"$(whoami | sed 's/\.//')"}
fi
# Test-run variables:
PROVIDER=${PROVIDER:-gcp}  # Provision using provided provider, or Google Cloud Platform by default.
NUM_HOSTS=${NUM_HOSTS:-10}
PLAYBOOK=${PLAYBOOK:-setup_weave-net_test.yml}
TESTS=${TESTS:-}
RUNNER_ARGS=${RUNNER_ARGS:-""}
# Dependencies' versions:
DOCKER_VERSION=${DOCKER_VERSION:-1.11.2}
KUBERNETES_VERSION=${KUBERNETES_VERSION:-1.5.1}
KUBERNETES_CNI_VERSION=${KUBERNETES_CNI_VERSION:-0.3.0.1}
# Lifecycle flags:
SKIP_CREATE=${SKIP_CREATE:-}
SKIP_CONFIG=${SKIP_CONFIG:-}
SKIP_DESTROY=${SKIP_DESTROY:-}

function print_vars() {
  echo "--- Variables: Main ---"
  echo "PROVIDER=$PROVIDER"
  echo "NUM_HOSTS=$NUM_HOSTS"
  echo "PLAYBOOK=$PLAYBOOK"
  echo "TESTS=$TESTS"
  echo "RUNNER_ARGS=$RUNNER_ARGS"
  echo "--- Variables: Versions ---"
  echo "DOCKER_VERSION=$DOCKER_VERSION"
  echo "KUBERNETES_VERSION=$KUBERNETES_VERSION"
  echo "KUBERNETES_CNI_VERSION=$KUBERNETES_CNI_VERSION"
  echo "--- Variables: Flags ---"
  echo "SKIP_CREATE=$SKIP_CREATE"
  echo "SKIP_CONFIG=$SKIP_CONFIG"
  echo "SKIP_DESTROY=$SKIP_DESTROY"
}

function verify_dependencies() {
  local deps=(python terraform ansible-playbook)
  for dep in "${deps[@]}"; do 
    if [ ! $(which $dep) ]; then 
      >&2 echo "$dep is not installed or not in PATH."
      exit 1
    fi
  done
}

function provision_locally() {
  case "$1" in
    on)
      vagrant up
      local status=$?
      eval $(vagrant ssh-config | sed \
        -ne 's/\ *HostName /ssh_hosts=/p' \
        -ne 's/\ *User /ssh_user=/p' \
        -ne 's/\ *Port /ssh_port=/p' \
        -ne 's/\ *IdentityFile /ssh_id_file=/p')
      return $status
      ;;
    off)
      vagrant destroy -f
      ;;
    *)
      >&2 echo "Unknown command $1. Usage: {on|off}."
      exit 1
      ;;
  esac
}

function update_local_etc_hosts() {
  # Remove old entries (if present):
  for host in $1; do sudo sed -i "/$host/d" /etc/hosts; done
  # Add new entries:
  sudo sh -c "echo \"$2\" >> /etc/hosts"
}

function upload_etc_hosts() {
  # Remove old entries (if present):
  $SSH $3 'for host in '$1'; do sudo sed -i "/$host/d" /etc/hosts; done'
  # Add new entries:
  echo "$2" | $SSH $3 "sudo -- sh -c \"cat >> /etc/hosts\""
}

function update_remote_etc_hosts() {
  local pids=""
  for host in $1; do
    upload_etc_hosts "$1" "$2" $host &
    local pids="$pids $!"
  done
  for pid in $pids; do wait $pid; done
}

function provision_remotely() {
  case "$1" in
    on)
      terraform apply -input=false -parallelism="$NUM_HOSTS" -var "app=$APP" -var "num_hosts=$NUM_HOSTS" "$DIR/../tools/provisioning/$2"
      local status=$?
      ssh_user=$(terraform output username)
      ssh_id_file=$(terraform output private_key_path)
      ssh_hosts=$(terraform output hostnames)
      return $status
      ;;
    off)
      terraform destroy -force "$DIR/../tools/provisioning/$2"
      ;;
    *)
      >&2 echo "Unknown command $1. Usage: {on|off}."
      exit 1
      ;;
  esac
}

function provision() {
  local action=$([ $1 == "on" ] && echo "Provisioning" || echo "Shutting down")
  echo; greenly echo "> $action test host(s) on [$PROVIDER]..."; local begin_prov=$(date +%s)
  case "$2" in
    aws)
      # TODO: set_up_for_aws
      provision_remotely $1 $2
      ;;
    do)
      set_up_for_do
      provision_remotely $1 $2
      ;;
    gcp)
      set_up_for_gcp
      provision_remotely $1 $2
      ;;
    vagrant)
      provision_locally $1
      ;;
    *)
      >&2 echo "Unknown provider $2. Usage: PROVIDER={gcp|aws|do|vagrant}."
      exit 1
      ;;
  esac

  export SSH="ssh -l $ssh_user -i $ssh_id_file -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
  # Set up /etc/hosts files on this ("local") machine and the ("remote") testing machines, to map hostnames and IP addresses, so that:
  # - this machine communicates with the testing machines via their public IPs;
  # - testing machines communicate between themselves via their private IPs;
  # - we can simply use just the hostname in all scripts to refer to machines, and the difference between public and private IP becomes transparent.
  # N.B.: if you decide to use public IPs everywhere, note that some tests may fail (e.g. test #115).
  update_local_etc_hosts "$ssh_hosts" "$(terraform output public_etc_hosts)"
  update_remote_etc_hosts "$ssh_hosts" "$(terraform output private_etc_hosts)"

  echo; greenly echo "> Provisioning took $(date -u -d @$(($(date +%s)-$begin_prov)) +"%T")."
}

function configure() {
  echo; greenly echo "> Configuring test host(s)..."; local begin_conf=$(date +%s)
  local inventory_file=$(mktemp /tmp/ansible_inventory_XXXXX)
  echo "[all]" > "$inventory_file"
  echo "$2" | sed "s/$/:$3/" >> "$inventory_file"

  ansible-playbook -u "$1" -i "$inventory_file" --private-key="$4" --forks="$NUM_HOSTS" \
    --ssh-extra-args="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
    --extra-vars "docker_version=$DOCKER_VERSION kubernetes_version=$KUBERNETES_VERSION kubernetes_cni_version=$KUBERNETES_CNI_VERSION" \
    "$DIR/../tools/config_management/$PLAYBOOK"

  echo; greenly echo "> Configuration took $(date -u -d @$(($(date +%s)-$begin_conf)) +"%T")."
}

function run_all() {
  echo; greenly echo "> Running tests..."; local begin_tests=$(date +%s)
  export COVERAGE=""
  export HOSTS="$(echo "$3" | tr '\n' ' ')"
  shift 3 # Drop the first 3 arguments, the remainder being, optionally, the list of tests to run.
  "$DIR/setup.sh"
  set +e
  "$DIR/run_all.sh" $@
  local status=$?
  echo; greenly echo "> Tests took $(date -u -d @$(($(date +%s)-$begin_tests)) +"%T")."
  return $status
}

begin=$(date +%s)
print_vars
verify_dependencies

provision on $PROVIDER
if [ $? -ne 0 ]; then
  >&2 echo "> Failed to provision test host(s)."
  exit 1
fi

if [ "$SKIP_CONFIG" != "yes" ]; then
  configure $ssh_user "$ssh_hosts" ${ssh_port:-22} $ssh_id_file
  if [ $? -ne 0 ]; then
    >&2 echo "Failed to configure test host(s)."
    exit 1
  fi
fi

run_all $ssh_user $ssh_id_file "$ssh_hosts" "$TESTS"
status=$?

if [ "$SKIP_DESTROY" != "yes" ]; then
  provision off $PROVIDER
fi

echo; greenly echo "> Build took $(date -u -d @$(($(date +%s)-$begin)) +"%T")."
exit $status
