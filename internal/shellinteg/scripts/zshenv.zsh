# tuios shell integration for zsh.
#
# tuios starts zsh with ZDOTDIR set to the folder of this file, so zsh reads
# this file in place of the user's .zshenv. It puts ZDOTDIR back as it was,
# reads the user's .zshenv, and in an interactive shell loads tuios.zsh, which
# adds the prompt marks. zsh then reads .zprofile, .zshrc and .zlogin from the
# user's own ZDOTDIR or home, as it always does. Child shells see the user's
# ZDOTDIR and none of this.
#
# Turn it off with [shell] integration = "off" in the tuios config.

'builtin' 'typeset' _tuios_dir=${${(%):-%x}:A:h}
if [[ -n "${TUIOS_ZSH_ZDOTDIR+X}" ]]; then
    'builtin' 'export' ZDOTDIR="$TUIOS_ZSH_ZDOTDIR"
    'builtin' 'unset' 'TUIOS_ZSH_ZDOTDIR'
else
    'builtin' 'unset' 'ZDOTDIR'
fi

{
    'builtin' 'typeset' _tuios_file=${ZDOTDIR-$HOME}"/.zshenv"
    [[ ! -r "$_tuios_file" ]] || 'builtin' 'source' '--' "$_tuios_file"
} always {
    if [[ -o 'interactive' && -r "$_tuios_dir/tuios.zsh" ]]; then
        'builtin' 'source' '--' "$_tuios_dir/tuios.zsh"
    fi
    'builtin' 'unset' '_tuios_file' '_tuios_dir'
}
