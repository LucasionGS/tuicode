# tuicode shell integration for bash, modelled on VS Code's: it reports prompts and commands
# with OSC 633 so the terminal can decorate and navigate them.
if [ -z "$TUICODE_SHELL_INTEGRATION_LOADED" ]; then
  TUICODE_SHELL_INTEGRATION_LOADED=1
  if [ -r ~/.bashrc ]; then
    . ~/.bashrc
  fi

  __tuicode_escape() {
    local s="${1//\\/\\\\}"
    s="${s//;/\\x3b}"
    s="${s//$'\n'/\\x0a}"
    printf '%s' "$s"
  }

  __tuicode_preexec() {
    local cmd
    cmd="$(HISTTIMEFORMAT= builtin history 1)"
    cmd="${cmd#*[0-9]*  }"
    printf '\e]633;E;%s\a\e]633;C\a' "$(__tuicode_escape "$cmd")"
  }

  __tuicode_status=0
  __tuicode_precmd_start() {
    __tuicode_status=$?
  }

  __tuicode_precmd_end() {
    printf '\e]633;D;%s\a\e]633;P;Cwd=%s\a' "$__tuicode_status" "$(__tuicode_escape "$PWD")"
    case "$PS1" in
      *'633;A'*) ;;
      *) PS1='\[\e]633;A\a\]'"$PS1"'\[\e]633;B\a\]' ;;
    esac
  }

  PS0='$(__tuicode_preexec)'"$PS0"
  PROMPT_COMMAND="__tuicode_precmd_start${PROMPT_COMMAND:+;$PROMPT_COMMAND};__tuicode_precmd_end"
fi
