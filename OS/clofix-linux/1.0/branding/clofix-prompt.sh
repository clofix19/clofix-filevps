# CloFix Linux shell prompt (sourced from /etc/profile.d)
# shellcheck shell=sh
# Root = red, normal user = green (standard Linux console look).
if [ -n "${BASH_VERSION:-}" ]; then
  if [ "$(id -u 2>/dev/null)" = "0" ]; then
    PS1='\[\e[01;31m\]\u@\h\[\e[00m\]:\[\e[01;34m\]\w\[\e[00m\]# '
  else
    PS1='\[\e[01;32m\]\u@\h\[\e[00m\]:\[\e[01;34m\]\w\[\e[00m\]\$ '
  fi
elif [ -n "${ZSH_VERSION:-}" ]; then
  if [ "$(id -u 2>/dev/null)" = "0" ]; then
    PS1='%F{red}%n@%m%f:%F{blue}%~%f# '
  else
    PS1='%F{green}%n@%m%f:%F{blue}%~%f%% '
  fi
else
  PS1='$(whoami)@$(hostname):$PWD\$ '
fi
# Ensure colorized tools work over SSH / browser PTY (TERM=xterm-256color).
export CLICOLOR=1
export CLOFIX_DISTRO="CloFix Linux"
export CLOFIX_DISTRO_VERSION="1.0"
