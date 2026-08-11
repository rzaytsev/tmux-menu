#!/bin/sh

set -eu

count_files() {
	if [ ! -d "$1" ]; then
		printf '0'
		return
	fi
	find "$1" -type f ! -name .gitkeep | awk 'END { print NR + 0 }'
}

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	git_changes=$(git status --porcelain=v1 2>/dev/null | awk 'END { print NR + 0 }')
	if [ "$git_changes" -eq 0 ]; then
		git_status=ok
		git_summary="Working tree clean"
	else
		git_status=warning
		git_summary="$git_changes changed files"
	fi
else
	git_status=unknown
	git_summary="Not a Git repository"
fi

printf '{"blocks":['
printf '{"title":"Git","status":"%s","summary":"%s"}' "$git_status" "$git_summary"

if [ -d todo/new ] || [ -d todo/doing ]; then
	new_count=$(count_files todo/new)
	doing_count=$(count_files todo/doing)
	if [ "$new_count" -gt 0 ]; then
		task_status=warning
	else
		task_status=ok
	fi
	printf ',{"title":"Tasks","status":"%s","summary":"%s new, %s doing"}' "$task_status" "$new_count" "$doing_count"
fi

printf ']}\n'
