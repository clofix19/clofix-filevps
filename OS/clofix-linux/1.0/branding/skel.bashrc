# ~/.bashrc - CloFix Linux defaults for new users
case $- in *i*) ;; *) return ;; esac
HISTFILE="${HISTFILE:-$HOME/.bash_history}"
HISTCONTROL=ignoreboth
HISTSIZE=1000
HISTFILESIZE=2000
export HISTFILE HISTSIZE HISTFILESIZE HISTCONTROL
shopt -s histappend 2>/dev/null || true
# Persist after every command so abrupt SSH disconnect / VPS stop does not lose history.
case ${PROMPT_COMMAND:-} in
  *history\ -a*) ;;
  *) PROMPT_COMMAND="history -a${PROMPT_COMMAND:+; $PROMPT_COMMAND}" ;;
esac
shopt -s checkwinsize 2>/dev/null || true
[ -x /usr/bin/dircolors ] && eval "$(dircolors -b)" 2>/dev/null || true
alias ls='ls --color=auto' 2>/dev/null || true
alias grep='grep --color=auto' 2>/dev/null || true
if [ -f /etc/profile.d/clofix-prompt.sh ]; then
  # shellcheck disable=SC1091
  . /etc/profile.d/clofix-prompt.sh
fi
