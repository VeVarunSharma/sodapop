#!/usr/bin/env bash

homebrew_formula_targets=(darwin-arm64 darwin-amd64 linux-arm64 linux-amd64)
homebrew_formula_platforms=(darwin/arm64 darwin/amd64 linux/arm64 linux/amd64)

homebrew_releasectl() {
  if [[ -n "${SODAPOP_RELEASECTL:-}" ]]; then
    "$SODAPOP_RELEASECTL" "$@"
  else
    go run ./scripts/releasectl "$@"
  fi
}

homebrew_canonical_path() {
  ruby -e '
    path = File.expand_path(ARGV.fetch(0))
    suffix = []
    until File.exist?(path) || File.dirname(path) == path
      suffix.unshift(File.basename(path))
      path = File.dirname(path)
    end
    path = File.realpath(path)
    suffix.each { |entry| path = File.join(path, entry) }
    puts path
  ' "$1"
}

homebrew_normalize_release_base() {
  ruby -rcgi -rpathname -ruri -e '
    base = ARGV.fetch(0).sub(%r{/+\z}, "")
    version = ARGV.fetch(1)
    github = "https://github.com/VeVarunSharma/sodapop/releases/download/v#{version}"
    if base == github
      puts github
      exit
    end

    if base.start_with?("file://")
      path = base.delete_prefix("file://")
      path = CGI.unescape(path)
      raise "file release base needs an existing directory" if path.empty?
      real = Pathname.new(path).realpath
      encoded = real.each_filename.map { |segment| CGI.escape(segment).gsub("+", "%20") }.join("/")
      puts "file:///" + encoded
      exit
    end

    uri = URI.parse(base)
    case uri.scheme
    when "http", "https"
      host = uri.host.to_s.downcase
      unless ["127.0.0.1", "localhost", "::1"].include?(host)
        raise "loopback HTTP(S) base required"
      end
      raise "query parameters are not supported" if uri.query
      raise "fragments are not supported" if uri.fragment
      path = uri.path.to_s.sub(%r{/+\z}, "")
      encoded_path = path.split("/").reject(&:empty?).map { |segment| CGI.escape(CGI.unescape(segment)).gsub("+", "%20") }.join("/")
      authority = +"#{uri.scheme}://"
      authority << if host.include?(":") && !host.start_with?("[")
        "[#{host}]"
      else
        host
      end
      authority << ":#{uri.port}" if uri.port && uri.port != uri.default_port
      authority << "/" unless encoded_path.empty?
      authority << encoded_path
      puts authority
    else
      raise "unsupported release base"
    end
  ' "$1" "$2" 2>/dev/null || {
    printf 'Homebrew release base must be the exact tagged GitHub release URL, a file:// URI, or a loopback HTTP(S) base: %s\n' "$1" >&2
    return 1
  }
}

homebrew_file_uri() {
  ruby -rcgi -rpathname -e '
    path = Pathname.new(File.realpath(ARGV.fetch(0)))
    encoded = path.each_filename.map { |segment| CGI.escape(segment).gsub("+", "%20") }.join("/")
    puts "file:///" + encoded
  ' "$1"
}

homebrew_escape_ruby_string() {
  ruby -e '
    value = ARGV.fetch(0)
    raise "unexpected newline in Homebrew string literal" if value.include?("\n")
    puts value.gsub(/["\\#]/) { |character| "\\#{character}" }
  ' "$1"
}

homebrew_escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[&|\\]/\\&/g'
}

homebrew_expected_archive() {
  local version="$1"
  local platform="$2"
  printf 'sodapop-%s-%s-%s.tar.gz' "$version" "${platform%/*}" "${platform#*/}"
}

homebrew_manifest_header() {
  ruby -rjson -e '
    data = JSON.parse(File.read(ARGV.fetch(0)))
    fields = %w[version copilot_sdk_version copilot_runtime_version].map do |name|
      value = data.fetch(name)
      raise "#{name} must be a non-empty string" unless value.is_a?(String) && !value.empty?
      value
    end
    puts fields.join("\t")
  ' "$1"
}

homebrew_manifest_artifact() {
  ruby -rjson -e '
    data = JSON.parse(File.read(ARGV.fetch(0)))
    platform = ARGV.fetch(1)
    matches = Array(data.fetch("artifacts")).select { |item| item.fetch("platform") == platform }
    raise "manifest must contain exactly one artifact for #{platform}" unless matches.length == 1
    artifact = matches.fetch(0)
    values = %w[archive archive_sha256 binary_sha256].map do |name|
      value = artifact.fetch(name)
      raise "#{name} must be a non-empty string" unless value.is_a?(String) && !value.empty?
      value
    end
    puts values.join("\t")
  ' "$1" "$2"
}

homebrew_source_pins() {
  local source="internal/runtimebundle/version.go"
  if [[ ! -f "$source" ]]; then
    printf 'Homebrew generation needs %s when no manifest is provided\n' "$source" >&2
    return 1
  fi
  local sdk_version runtime_version
  sdk_version="$(sed -n 's/^[[:space:]]*SDKVersion[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$source")"
  runtime_version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$source")"
  if [[ -z "$sdk_version" || -z "$runtime_version" ]]; then
    printf 'Could not read the pinned Copilot SDK/runtime versions from %s\n' "$source" >&2
    return 1
  fi
  printf '%s\t%s\n' "$sdk_version" "$runtime_version"
}

homebrew_sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}
