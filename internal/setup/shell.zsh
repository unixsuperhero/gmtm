# GMTM_SETUP_ID=zsh
# OSC 133 boundaries and durable history for the selected TUIOS server.
[[ -o interactive && ${TUIOS_SERVER-} == __SERVER__ && -n ${TUIOS_PANE_ID-} && -n ${TUIOS_SESSION-} ]] || return 0
[[ -z ${_GMTM_OSC133_INSTALLED-} ]] || return 0
_GMTM_OSC133_INSTALLED=1
autoload -Uz add-zsh-hook
_gmtm_capture_preexec() { _GMTM_LAST_COMMAND=$1; }
_gmtm_capture_sync() {
  local -a command_args=()
  if (( ${+_GMTM_LAST_COMMAND} )); then
    command_args=(--command "$_GMTM_LAST_COMMAND")
  fi
  __BINARY__ --server __SERVER__ --db __DB__ capture sync --session "$TUIOS_SESSION" --window "$TUIOS_PANE_ID" "${command_args[@]}"
  unset _GMTM_LAST_COMMAND
}
add-zsh-hook preexec _gmtm_capture_preexec
_gmtm_osc133_precmd() {
  local command_exit=$?
  print -n "\e]133;D;$command_exit\a"
  _gmtm_capture_sync
  print -n "\e]133;A\a"
  return "$command_exit"
}
_gmtm_capture_precmd() {
  local command_exit=$?
  _gmtm_capture_sync
  return "$command_exit"
}
_gmtm_osc133_preexec() { print -n "\e]133;C\a"; }
# Keep an existing native/tuios integration and add only persistence after it.
if (( ${+functions[_tuios_osc133_precmd]} )) || [[ $PS1 == *']133;'* ]]; then
  add-zsh-hook precmd _gmtm_capture_precmd
else
  add-zsh-hook precmd _gmtm_osc133_precmd
  add-zsh-hook preexec _gmtm_osc133_preexec
  PS1+=$'%{\e]133;B\a%}'
fi
