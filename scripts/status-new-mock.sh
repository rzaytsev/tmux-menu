#!/bin/sh
set -eu

if [ "${1:-}" = "--preview" ]; then
	file=${2:-}
	if [ -z "$file" ] || [ ! -f "$file" ]; then
		exit 0
	fi
	printf '\033[1m%s\033[0m\n\n' "$file"
	if command -v glow >/dev/null 2>&1; then
		exec glow "$file"
	fi
	if command -v bat >/dev/null 2>&1; then
		exec bat --style=plain --color=always "$file"
	fi
	exec sed -n '1,240p' "$file"
fi

if ! command -v fzf >/dev/null 2>&1; then
	echo "tmux-menu: fzf is required for status-new mock" >&2
	exit 1
fi

session_root=${STATUS_SESSION_ROOT:-${TMUX_MENU_SESSION_PATH:-}}
if [ -z "$session_root" ] && [ -n "${TMUX:-}" ] && command -v tmux >/dev/null 2>&1; then
	session_root=$(tmux display-message -p '#{session_path}' 2>/dev/null || true)
fi
if [ -z "$session_root" ]; then
	session_root=$(pwd)
fi
todo_root=${STATUS_TODO_ROOT:-"$session_root/todo"}
using_demo=0

has_board() {
	[ -d "$1/new" ] && [ -d "$1/doing" ] && [ -d "$1/done" ]
}

seed_demo() {
	demo_root="${TMPDIR:-/tmp}/tmux-menu-status-new-demo"
	todo_root="$demo_root/todo"
	mkdir -p "$todo_root/new" "$todo_root/doing" "$todo_root/done"
	cat >"$todo_root/new/01-shape-status-board.md" <<-'TASK'
# Shape status board
summary: Decide the task row layout, preview toggle, and editor open behavior.

Acceptance:
- The board keeps one selectable task per row.
- Space toggles the full Markdown preview.
- Enter opens the selected file in the editor.
TASK
	cat >"$todo_root/doing/02-test-with-real-tasks.md" <<-'TASK'
# Test with real tasks
summary: Run the mock against a real todo/new, todo/doing, todo/done tree.

Notes:
- Keep this mock shell-only until the interaction feels right.
- Production code can move parsing into Go after the design is stable.
TASK
	cat >"$todo_root/done/03-keep-old-status-safe.md" <<-'TASK'
# Keep old status safe
summary: Do not replace the production status view before reviewing the mock.

Done when:
- make status-new exists.
- make status still uses the current implementation.
TASK
}

if ! has_board "$todo_root"; then
	using_demo=1
	seed_demo
fi

rows_file=$(mktemp "${TMPDIR:-/tmp}/tmux-menu-status-new.XXXXXX")
trap 'rm -f "$rows_file"' EXIT INT TERM

esc=$(printf '\033')
reset="${esc}[0m"
bold="${esc}[1m"
dim="${esc}[2m"
new_color="${esc}[36m"
doing_color="${esc}[33m"
done_color="${esc}[32m"

clean_one_line() {
	printf '%s' "$1" | tr '\t\r\n' '   ' | sed 's/[[:space:]][[:space:]]*/ /g; s/^ //; s/ $//'
}

truncate() {
	text=$1
	max=$2
	if [ "${#text}" -le "$max" ]; then
		printf '%s' "$text"
		return
	fi
	cut_at=$((max - 3))
	printf '%s...' "$(printf '%s' "$text" | cut -c 1-"$cut_at")"
}

task_title() {
	file=$1
	title=$(basename "$file")
	title=${title%.*}
	title=$(printf '%s' "$title" | tr '_-' '  ')
	clean_one_line "$title"
}

task_summary() {
	file=$1
	summary=$(sed -n 's/^[[:space:]]*[Ss][Uu][Mm][Mm][Aa][Rr][Yy]:[[:space:]]*//p' "$file" | sed -n '1p')
	if [ -z "$summary" ]; then
		summary="(no summary)"
	fi
	clean_one_line "$summary"
}

append_lane() {
	lane=$1
	label=$2
	color=$3
	dir="$todo_root/$lane"
	find "$dir" -type f | sort | while IFS= read -r file; do
		case "$(basename "$file")" in
			.gitkeep) continue ;;
		esac
		title=$(truncate "$(task_title "$file")" 38)
		summary=$(truncate "$(task_summary "$file")" 72)
		display=$(printf '%b%-6s%b | %b%-38s%b | %-72s' "$color" "$label" "$reset" "$bold" "$title" "$reset" "$summary")
		printf '%s\t%s\n' "$file" "$display"
	done >>"$rows_file"
}

append_lane "new" "NEW" "$new_color"
append_lane "doing" "DOING" "$doing_color"
append_lane "done" "DONE" "$done_color"

if [ ! -s "$rows_file" ]; then
	echo "tmux-menu: no task files under $todo_root/{new,doing,done}" >&2
	exit 0
fi

if [ "$using_demo" -eq 1 ]; then
	echo "tmux-menu: no todo/new|doing|done under $session_root; using demo at $todo_root" >&2
fi

script_path=$0
selection=$(
	fzf \
		--ansi \
		--height=100% \
		--layout=reverse \
		--prompt='status board> ' \
		--delimiter='	' \
		--with-nth=2 \
		--header='NEW / DOING / DONE | Space preview | Enter edit | Ctrl-C cancel' \
		--preview="sh $script_path --preview {1}" \
		--preview-window='right:60%:hidden:wrap' \
		--bind='space:toggle-preview' \
		<"$rows_file" || true
)

if [ -z "$selection" ]; then
	exit 0
fi

selected_file=$(printf '%s\n' "$selection" | sed -n '1s/	.*//p')
if [ -z "$selected_file" ]; then
	exit 0
fi

editor=${EDITOR:-${VISUAL:-vi}}
exec sh -c 'exec "$@"' sh $editor "$selected_file"
