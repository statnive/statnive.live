#!/usr/bin/env bash
# Should NOT trigger chaos-script-up-down-status — sources the shared
# lib and calls dispatch (which provides up/down/status).
source "$(dirname "$0")/_lib.sh"

scenario_up()    { echo "up";   }
scenario_down()  { echo "down"; }
scenario_status(){ echo "status"; }

dispatch X "$@"