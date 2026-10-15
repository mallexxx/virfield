#import <UIKit/UIKit.h>
#import "LegacySupport.h"
int main(int argc, char *argv[]) {
    @autoreleasepool {
        if (VirfieldLegacySum(@[@1, @2, @3]) != 6) return 1;
        return UIApplicationMain(argc, argv, nil, nil);
    }
}
