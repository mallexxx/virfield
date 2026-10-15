set -eu
set +x
prefix=/opt/virfield/toolchains/legacy-ios-v1
script_dir=$(cd "$(/usr/bin/dirname "$0")" && pwd -P)
/bin/bash "$script_dir/check-platform.sh"
. "$script_dir/env.sh"
if [ "$(/usr/bin/uname -m)" = arm64 ]; then /usr/bin/arch -x86_64 /usr/bin/true; fi
bundle _2.5.22_ exec ruby "$script_dir/verify.rb"
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
ruby "$script_dir/native_arch.rb" "$app" armv7
[ "$(/usr/bin/lipo -archs "$app")" = armv7 ]
/usr/bin/nm -g "$app" | /usr/bin/grep -q ' T _VirfieldLegacySum$'
/usr/bin/dwarfdump --uuid "$app"
/usr/bin/shasum -a 256 "$app" Podfile.lock "$prefix/bundle/Gemfile.lock" "$prefix/profile.sha256"
printf 'virfield-legacy-ios-armv7-ok\n'
