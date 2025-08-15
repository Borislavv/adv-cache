#!/usr/bin/env bash
set -euo pipefail

APP_BIN=${APP_BIN:-"./advCache"}
GRAFTERM=${GRAFTERM:-"grafterm"}
GRAFTERM_ARGS=${GRAFTERM_ARGS:-"--config ./dashboard.json --datasource.prometheus.url http://localhost:9090"}

"$APP_BIN" & APP_PID=$!
"$GRAFTERM" $GRAFTERM_ARGS & GRAF_PID=$!

trap 'kill -TERM $APP_PID $GRAF_PID 2>/dev/null || true' INT TERM

wait -n $APP_PID $GRAF_PID
exit_code=$?
kill -TERM $APP_PID $GRAF_PID 2>/dev/null || true
wait $APP_PID $GRAF_PID 2>/dev/null || true
exit $exit_code
