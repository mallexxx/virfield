export LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8
export VIRFIELD_LEGACY_IOS_HOME=/opt/virfield/toolchains/legacy-ios-v1
export PATH="$VIRFIELD_LEGACY_IOS_HOME/ruby/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export GEM_HOME="$VIRFIELD_LEGACY_IOS_HOME/gems"
export GEM_PATH="$GEM_HOME:$VIRFIELD_LEGACY_IOS_HOME/ruby/lib/ruby/gems/3.3.0"
export BUNDLE_GEMFILE="$VIRFIELD_LEGACY_IOS_HOME/bundle/Gemfile"
export BUNDLE_PATH="$GEM_HOME" BUNDLE_APP_CONFIG="$VIRFIELD_LEGACY_IOS_HOME/bundle/.bundle"
export BUNDLE_FORCE_RUBY_PLATFORM=true BUNDLE_FROZEN=true
