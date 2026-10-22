# GMTM_SETUP_ID=bash
# OSC 133 marks for the selected TUIOS server only; bash 4.4+.
[[ $- == *i* && ${TUIOS_SERVER-} == __SERVER__ && -n ${TUIOS_PANE_ID-} && -n ${TUIOS_SESSION-} ]] || return 0
(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )) || return 0
[[ -z ${_GMTM_OSC133_INSTALLED-} ]] || return 0
# Preserve an existing OSC 133 integration and append only persistence.
_GMTM_EXISTING_OSC133=
[[ ${PS0-}${PS1-}${PROMPT_COMMAND[*]-} != *']133;'* ]] || _GMTM_EXISTING_OSC133=1
_GMTM_OSC133_INSTALLED=1
_GMTM_HISTORY_NUMBER=$HISTCMD
_gmtm_capture_sync() {
  local -a command_args=()
  local command_text
  # Only a newly added history entry can belong to the command just finished.
  # Ignored/disabled history must not substitute the preceding command.
  if [[ -o history ]] && (( HISTCMD == _GMTM_HISTORY_NUMBER + 1 )); then
    if command_text=$(HISTTIMEFORMAT= builtin history 1 && printf '\001'); then
      command_text=${command_text%$'\001'}
      command_text=${command_text%$'\n'}
      if [[ $command_text =~ ^[[:blank:]]*[0-9]+[[:blank:]*][[:blank:]] ]]; then
        command_text=${command_text#"${BASH_REMATCH[0]}"}
        command_args=(--command "$command_text")
      fi
    fi
  fi
  _GMTM_HISTORY_NUMBER=$HISTCMD
  __BINARY__ --server __SERVER__ --db __DB__ capture sync --session "$TUIOS_SESSION" --window "$TUIOS_PANE_ID" "${command_args[@]}"
}
_gmtm_osc133_precmd() {
  local status=$?
  printf '\033]133;D;%s\007' "$status"
  _gmtm_capture_sync
  printf '\033]133;A\007'
  return "$status"
}
_gmtm_capture_precmd() {
  local status=$?
  _gmtm_capture_sync
  return "$status"
}
if [[ -n $_GMTM_EXISTING_OSC133 ]]; then
  if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == 'declare -a '* ]]; then
    PROMPT_COMMAND+=(_gmtm_capture_precmd)
  else
    PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND;}_gmtm_capture_precmd"
  fi
  return 0
fi
if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == 'declare -a '* ]]; then
  PROMPT_COMMAND=(_gmtm_osc133_precmd "${PROMPT_COMMAND[@]}")
else
  PROMPT_COMMAND="_gmtm_osc133_precmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
fi
PS0='\e]133;C\a'"${PS0-}"
PS1+='\[\e]133;B\a\]'
