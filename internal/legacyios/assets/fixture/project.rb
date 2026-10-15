require 'xcodeproj'
project = Xcodeproj::Project.new('LegacyIOS.xcodeproj')
target = project.new_target(:application, 'LegacyIOS', :ios, '9.0')
file = project.main_group.new_file('App/main.m')
target.source_build_phase.add_file_reference(file)
(project.build_configurations + target.build_configurations).each do |config|
  config.build_settings['ARCHS'] = 'armv7'
  config.build_settings['ONLY_ACTIVE_ARCH'] = 'NO'
  config.build_settings['CODE_SIGNING_ALLOWED'] = 'NO'
  config.build_settings['GENERATE_INFOPLIST_FILE'] = 'YES'
  config.build_settings['PRODUCT_BUNDLE_IDENTIFIER'] = 'ai.virfield.LegacyIOS'
  config.build_settings['CLANG_ENABLE_OBJC_ARC'] = 'YES'
end
project.save
