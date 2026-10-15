require 'json'
require 'rbconfig'
require 'openssl'
require 'psych'
require 'ffi'
require 'ethon'
require 'cocoapods'
require_relative 'native_arch'
prefix = '/opt/virfield/toolchains/legacy-ios-v1'
abort 'Legacy iOS requires the profile UTF-8 environment' unless Encoding.default_external == Encoding::UTF_8
manifest = JSON.parse(File.read("#{prefix}/manifest.json"))
abort 'Ruby version mismatch' unless RUBY_VERSION == manifest.fetch('ruby')
abort 'OpenSSL version mismatch' unless OpenSSL::OPENSSL_LIBRARY_VERSION.start_with?('OpenSSL 3.5.9 ')
abort 'LibYAML version mismatch' unless Psych.libyaml_version == [0, 2, 5]
manifest.fetch('gems').each do |entry|
  spec = Gem.loaded_specs[entry.fetch('name')] || Gem::Specification.find_by_name(entry.fetch('name'))
  abort "gem mismatch: #{spec.name}" unless spec.version.to_s == entry.fetch('version') && spec.platform.to_s == 'ruby' && spec.full_gem_path.start_with?(prefix + '/')
end
native = `uname -m`.strip
abort 'Ruby/native architecture mismatch' unless RbConfig::CONFIG.fetch('host_cpu') == native
extension = $LOADED_FEATURES.find { |path| path.end_with?('/ffi_c.bundle') }
abort 'FFI native extension not loaded' unless extension && extension.start_with?(prefix + '/')
abort 'FFI Mach-O architecture mismatch' unless VirfieldLegacyIOS.verify_macho(extension, native)
version = Ethon::Curl.version
abort 'Ethon libcurl did not load' unless version.start_with?('libcurl/')
puts JSON.generate(profile: manifest.fetch('profile'), ruby: RUBY_VERSION, ruby_platform: RUBY_PLATFORM, architecture: native, cocoapods: Pod::VERSION, ffi: FFI::VERSION, ethon: Ethon::VERSION, ffi_library: extension, libcurl: version, openssl: OpenSSL::OPENSSL_LIBRARY_VERSION)
