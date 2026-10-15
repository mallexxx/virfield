module VirfieldLegacyIOS
  def self.verify_macho(file, architecture)
    system('/usr/bin/lipo', file, '-verify_arch', architecture, out: File::NULL)
  end
end

if $PROGRAM_NAME == __FILE__
  abort 'Expected a Mach-O file and architecture' unless ARGV.length == 2
  exit(VirfieldLegacyIOS.verify_macho(ARGV[0], ARGV[1]) ? 0 : 1)
end
