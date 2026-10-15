set -eu
set +x
umask 022
export LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8
assets=$1
/bin/bash "$assets/check-platform.sh"
[ "$(/usr/bin/id -u)" = 0 ]
prefix=/opt/virfield/toolchains/legacy-ios-v1
if [ -e "$prefix" ]; then
  [ -f "$prefix/profile.sha256" ] && /usr/bin/cmp -s "$assets/profile.sha256" "$prefix/profile.sha256" && exit 0
  echo 'Existing or incomplete profile preserved; create a fresh image instead of overwriting it' >&2
  exit 1
fi
/bin/mkdir -p /opt/virfield/toolchains
/bin/mkdir "$prefix"
/bin/chmod 755 "$prefix"
work=$(/usr/bin/mktemp -d /private/tmp/virfield-legacy-build.XXXXXX)
build_log="$prefix/build.log"
exec 3>&1
exec > "$build_log" 2>&1
/bin/chmod 600 "$build_log"
trap 'status=$?; if [ "$status" -ne 0 ]; then /usr/bin/tail -n 100 "$build_log" >&3; fi; /bin/rm -rf "$work"; exit "$status"' EXIT
printf 'Verifying pinned source downloads\n' >&3
while IFS="$(printf '\t')" read -r hash url file; do
  /usr/bin/curl --silent --show-error --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --retry 2 --connect-timeout 30 --max-time 600 --max-filesize 100000000 "$url" -o "$work/$file"
  [ "$(/usr/bin/shasum -a 256 "$work/$file" | /usr/bin/awk '{print $1}')" = "$hash" ] || { echo "SHA256 mismatch: $file" >&2; exit 1; }
done < "$assets/downloads.tsv"
if [ "$(/usr/bin/uname -m)" = arm64 ]; then
  if ! /usr/bin/arch -x86_64 /usr/bin/true 2>/dev/null; then
    /usr/sbin/softwareupdate --install-rosetta --agree-to-license
  fi
  /usr/bin/arch -x86_64 /usr/bin/true
fi
jobs=$(/usr/sbin/sysctl -n hw.ncpu)
[ "$jobs" -le 4 ] || jobs=4
cd "$work"
printf 'Building native LibYAML\n' >&3
/usr/bin/tar -xzf yaml-0.2.5.tar.gz
(cd yaml-0.2.5 && ./configure --prefix="$prefix/libyaml" && /usr/bin/make -j "$jobs" && /usr/bin/make install)
/usr/bin/tar -xzf openssl-3.5.9.tar.gz
printf 'Building native OpenSSL LTS\n' >&3
case "$(/usr/bin/uname -m)" in arm64) openssl_arch=darwin64-arm64-cc ;; x86_64) openssl_arch=darwin64-x86_64-cc ;; esac
(cd openssl-3.5.9 && ./Configure "$openssl_arch" --prefix="$prefix/openssl" --openssldir="$prefix/openssl/ssl" no-tests && /usr/bin/make -j "$jobs" && /usr/bin/make install_sw)
/bin/mkdir -p "$prefix/openssl/ssl"
/usr/bin/security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > "$prefix/openssl/ssl/cert.pem"
/usr/bin/tar -xzf ruby-3.3.12.tar.gz
printf 'Building native Ruby\n' >&3
(cd ruby-3.3.12 && ./configure --prefix="$prefix/ruby" --enable-shared --disable-install-doc --disable-yjit --without-gmp --with-openssl-dir="$prefix/openssl" --with-libyaml-dir="$prefix/libyaml" && /usr/bin/make -j "$jobs" && /usr/bin/make install)
/bin/mkdir "$prefix/bundle"
/bin/cp "$assets/Gemfile" "$assets/Gemfile.lock" "$prefix/bundle/"
/bin/mkdir "$prefix/bundle/vendor" "$prefix/bundle/vendor/cache"
/bin/cp "$work/"*.gem "$prefix/bundle/vendor/cache/"
. "$assets/env.sh"
ruby -e 'require "openssl"; require "psych"; abort unless RUBY_VERSION == "3.3.12"'
printf 'Installing frozen source gems\n' >&3
bundle _2.5.22_ install --local
/bin/cp -R "$assets/fixture" "$prefix/fixture"
/bin/mkdir "$prefix/licenses"
/bin/cp "$work/ruby-3.3.12/COPYING" "$prefix/licenses/Ruby-COPYING"
/bin/cp "$work/openssl-3.5.9/LICENSE.txt" "$prefix/licenses/OpenSSL-LICENSE.txt"
/bin/cp "$work/yaml-0.2.5/License" "$prefix/licenses/LibYAML-License"
/bin/cp "$assets/check-platform.sh" "$assets/verify.rb" "$assets/verify.sh" "$assets/native_arch.rb" "$assets/env.sh" "$assets/manifest.json" "$prefix/"
/bin/chmod -R a+rX,go-w "$prefix"
/bin/chmod 600 "$build_log"
/bin/cp "$assets/profile.sha256" "$prefix/profile.sha256"
printf 'legacy-ios-v1-installed\n' >&3
