# tuicode shell integration for fish, modelled on VS Code's: it reports prompts and commands
# with OSC 633 so the terminal can decorate and navigate them.
if not set -q TUICODE_SHELL_INTEGRATION_LOADED
  set -g TUICODE_SHELL_INTEGRATION_LOADED 1

  function __tuicode_escape
    string replace -a '\\' '\\\\' -- $argv | string replace -a ';' '\\x3b' | string join '\\x0a'
  end

  function __tuicode_prompt --on-event fish_prompt
    printf '\e]633;P;Cwd=%s\a\e]633;A\a' (__tuicode_escape $PWD)
  end

  function __tuicode_preexec --on-event fish_preexec
    printf '\e]633;E;%s\a\e]633;C\a' (__tuicode_escape $argv)
  end

  function __tuicode_postexec --on-event fish_postexec
    printf '\e]633;D;%s\a' $status
  end
end
