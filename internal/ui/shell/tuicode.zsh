# tuicode shell integration for zsh, modelled on VS Code's: it reports prompts and commands
# with OSC 633 so the terminal can decorate and navigate them.
ZDOTDIR="${TUICODE_USER_ZDOTDIR:-$HOME}"
if [ -r "$ZDOTDIR/.zshrc" ]; then
  . "$ZDOTDIR/.zshrc"
fi

__tuicode_escape() {
  local s="${1//\\/\\\\}"
  s="${s//;/\\x3b}"
  s="${s//$'\n'/\\x0a}"
  print -rn -- "$s"
}

__tuicode_precmd() {
  local ret=$?
  print -n "\e]633;D;$ret\a\e]633;P;Cwd=$(__tuicode_escape "$PWD")\a"
  if [[ "$PS1" != *'633;A'* ]]; then
    PS1=$'%{\e]633;A\a%}'"$PS1"$'%{\e]633;B\a%}'
  fi
}

__tuicode_preexec() {
  print -n "\e]633;E;$(__tuicode_escape "$1")\a\e]633;C\a"
}

autoload -Uz add-zsh-hook
add-zsh-hook precmd __tuicode_precmd
add-zsh-hook preexec __tuicode_preexec
