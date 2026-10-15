#import "LegacySupport.h"
NSInteger VirfieldLegacySum(NSArray<NSNumber *> *values) {
    NSInteger result = 0;
    for (NSNumber *value in values) result += value.integerValue;
    return result;
}
