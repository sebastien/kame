# fd 3 receives NUL-delimited requests, fd 4 replies. stdin stays closed.
_kash_bridge_read() { IFS= builtin read -r -d '' "$1" <&3; }
_kash_bridge_edit() {
	local _kash_bridge_key=${2%%=*} _kash_bridge_declaration _kash_bridge_flags
	[[ $_kash_bridge_key =~ ^[a-zA-Z_][a-zA-Z_0-9]*$ && $_kash_bridge_key != _kash_bridge_* ]] || return 1
	if _kash_bridge_declaration=$(builtin declare -p -- "$_kash_bridge_key" 2>/dev/null); then
		_kash_bridge_flags=${_kash_bridge_declaration#declare -}
		_kash_bridge_flags=${_kash_bridge_flags%% *}
		# Avoid arithmetic interpretation, name references and coercion of data.
		[[ $_kash_bridge_flags != *[aAilunr]* ]] || return 1
	fi
	if [[ $1 == set ]]; then
		builtin export -- "$2"
	else
		builtin unset -v -- "$2"
	fi
}
_kash_bridge_snapshot() {
	local _kash_bridge_key _kash_bridge_declaration _kash_bridge_flags
	while IFS= builtin read -r _kash_bridge_key; do
		case $_kash_bridge_key in
			_kash_bridge_*|SHLVL|PWD|OLDPWD|_|BASH_ENV|BASHOPTS|SHELLOPTS) continue ;;
		esac
		[[ $_kash_bridge_key =~ ^[a-zA-Z_][a-zA-Z_0-9]*$ ]] || continue
		_kash_bridge_declaration=$(builtin declare -p -- "$_kash_bridge_key") || return
		_kash_bridge_flags=${_kash_bridge_declaration#declare -}
		_kash_bridge_flags=${_kash_bridge_flags%% *}
		[[ $_kash_bridge_flags == *a* || $_kash_bridge_flags == *A* || $_kash_bridge_flags == *n* ]] && continue
		[[ ${!_kash_bridge_key+x} ]] || continue
		builtin printf '%s=%s\0' "$_kash_bridge_key" "${!_kash_bridge_key}"
	done < <(builtin compgen -e)
	builtin printf '\0'
}

readonly _kash_bridge_out=$1 _kash_bridge_err=$2
set --
while _kash_bridge_read _kash_bridge_mode; do
	_kash_bridge_read _kash_bridge_directory &&
		_kash_bridge_read _kash_bridge_command || exit 125
	while _kash_bridge_read _kash_bridge_value && [[ -n $_kash_bridge_value ]]; do
		_kash_bridge_edit set "$_kash_bridge_value" || exit 125
	done
	while _kash_bridge_read _kash_bridge_value && [[ -n $_kash_bridge_value ]]; do
		_kash_bridge_edit unset "$_kash_bridge_value" || exit 125
	done
	_kash_bridge_read _kash_bridge_count || exit 125
	_kash_bridge_args=()
	while (( _kash_bridge_count > 0 )); do
		_kash_bridge_read _kash_bridge_value || exit 125
		_kash_bridge_args+=("$_kash_bridge_value")
		_kash_bridge_count=$((_kash_bridge_count - 1))
	done

	{
		builtin cd -- "$_kash_bridge_directory" || exit 125
		_kash_bridge_snapshot >&4 || exit 125
		_kash_bridge_status=0
		if [[ $_kash_bridge_mode == source ]]; then
			builtin source "$_kash_bridge_command" || _kash_bridge_status=$?
		else
			if ! builtin declare -F -- "$_kash_bridge_command" >/dev/null; then
				_kash_bridge_status=127
			else
				"$_kash_bridge_command" ${_kash_bridge_args[@]+"${_kash_bridge_args[@]}"} || _kash_bridge_status=$?
			fi
		fi
		_kash_bridge_snapshot >&4 || exit 125
		if [[ $_kash_bridge_mode == source ]]; then
			while IFS= builtin read -r _kash_bridge_name; do
				case $_kash_bridge_name in
					_kash_bridge_*|*/*) continue ;;
				esac
				builtin printf '%s\0' "$_kash_bridge_name" >&4
			done < <(builtin compgen -A function)
		fi
		builtin printf '\0%s\0' "$_kash_bridge_status" >&4
	} >"$_kash_bridge_out" 2>"$_kash_bridge_err" || exit 125
done
