# Load AFTER TPM so this explicit binding wins over tmux-fzf-complete.
# Only the prefix table is changed; Ctrl-T in the application stays available.
bind-key C-t run-shell 'TMUX_MENU_ORIGIN_PANE=#{q:pane_id} TMUX_MENU_ORIGIN_CLIENT=#{q:client_name} "$HOME/.local/bin/tmux-menu" popup complete'
