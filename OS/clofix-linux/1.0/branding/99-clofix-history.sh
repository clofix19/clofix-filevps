# CloFix Linux — persist bash history after every command.
# Abrupt SSH disconnect / VPS stop otherwise loses in-memory history.
# shellcheck shell=sh
if [ -n "${BASH_VERSION:-}" ]; then
  HISTFILE="${HISTFILE:-$HOME/.bash_history}"
  HISTSIZE="${HISTSIZE:-1000}"
  HISTFILESIZE="${HISTFILESIZE:-2000}"
  HISTCONTROL="${HISTCONTROL:-ignoreboth}"
  export HISTFILE HISTSIZE HISTFILESIZE HISTCONTROL
  shopt -s histappend 2>/dev/null || true
  case ${PROMPT_COMMAND:-} in
    *history\ -a*) ;;
    *) PROMPT_COMMAND="history -a${PROMPT_COMMAND:+; $PROMPT_COMMAND}" ;;
  esac
fi
