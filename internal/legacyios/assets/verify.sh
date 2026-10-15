set -eu
set +x
prefix=/opt/virfield/toolchains/legacy-ios-v1
/bin/bash "$prefix/check-platform.sh"
export PATH="$prefix/ruby/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export GEM_HOME="$prefix/gems" GEM_PATH="$prefix/gems:$prefix/ruby/lib/ruby/gems/3.3.0"
export BUNDLE_GEMFILE="$prefix/bundle/Gemfile" BUNDLE_PATH="$prefix/gems" BUNDLE_APP_CONFIG="$prefix/bundle/.bundle" BUNDLE_FORCE_RUBY_PLATFORM=true BUNDLE_FROZEN=true
if [ "$(/usr/bin/uname -m)" = arm64 ]; then /usr/bin/arch -x86_64 /usr/bin/true; fi
bundle _2.5.22_ exec ruby "$prefix/verify.rb"
work=$(/usr/bin/mktemp -d /private/tmp/virfield-legacy-verify.XXXXXX)
trap '/bin/rm -rf "$work"' EXIT
/bin/cp -R "$prefix/fixture/." "$work/"
cd "$work"
bundle _2.5.22_ exec ruby project.rb
before=$(/usr/bin/shasum -a 256 Podfile.lock | /usr/bin/awk '{print $1}')
COCOAPODS_DISABLE_STATS=true bundle _2.5.22_ exec pod install --deployment --no-repo-update
[ "$(/usr/bin/shasum -a 256 Podfile.lock | /usr/bin/awk '{print $1}')" = "$before" ]
/usr/bin/xcodebuild -workspace LegacyIOS.xcworkspace -scheme LegacyIOS -configuration Release -sdk iphoneos -destination 'generic/platform=iOS' -derivedDataPath "$work/DerivedData" ARCHS=armv7 ONLY_ACTIVE_ARCH=NO CODE_SIGNING_ALLOWED=NO build > build.log 2>&1 || { /usr/bin/tail -n 60 build.log; exit 1; }
app="$work/DerivedData/Build/Products/Release-iphoneos/LegacyIOS.app/LegacyIOS"
/usr/bin/lipo -verify_arch armv7 "$app"
[ "$(/usr/bin/lipo -archs "$app")" = armv7 ]
/usr/bin/nm -g "$app" | /usr/bin/grep -q ' T _VirfieldLegacySum$'
/usr/bin/dwarfdump --uuid "$app"
/usr/bin/shasum -a 256 "$app" Podfile.lock "$prefix/bundle/Gemfile.lock" "$prefix/profile.sha256"
printf 'virfield-legacy-ios-armv7-ok\n'
