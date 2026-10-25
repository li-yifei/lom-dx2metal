// Metal 编译/链接/反射，逻辑与 tools/lom-arm64-converter/vendor/metal-tools/reflect_batch.m 一致，
// 只是把"读 stdin 路径、写 stdout"换成函数调用：库由 Go 侧缓存，结果仍编码为同样的 JSON。
#import <Foundation/Foundation.h>
#import <Metal/Metal.h>
#include <stdlib.h>
#include <string.h>
#include "reflect.h"

static NSDictionary *typeInfo(MTLDataType type) {
    switch (type) {
#define TYPE(t, base, rows, cols) case MTLDataType##t: \
    return @{@"type": @#base, @"rows": @rows, @"cols": @cols};
        TYPE(Float, float, 1, 1) TYPE(Float2, float, 1, 2)
        TYPE(Float3, float, 1, 3) TYPE(Float4, float, 1, 4)
        TYPE(Float2x2, float, 2, 2) TYPE(Float2x3, float, 3, 2)
        TYPE(Float2x4, float, 4, 2) TYPE(Float3x2, float, 2, 3)
        TYPE(Float3x3, float, 3, 3) TYPE(Float3x4, float, 4, 3)
        TYPE(Float4x2, float, 2, 4) TYPE(Float4x3, float, 3, 4)
        TYPE(Float4x4, float, 4, 4)
        TYPE(Int, int, 1, 1) TYPE(Int2, int, 1, 2)
        TYPE(Int3, int, 1, 3) TYPE(Int4, int, 1, 4)
        TYPE(UInt, uint, 1, 1) TYPE(UInt2, uint, 1, 2)
        TYPE(UInt3, uint, 1, 3) TYPE(UInt4, uint, 1, 4)
        TYPE(Half, half, 1, 1) TYPE(Half2, half, 1, 2)
        TYPE(Half3, half, 1, 3) TYPE(Half4, half, 1, 4)
#undef TYPE
        default: return @{@"type": @"unsupported", @"metal_type": @(type)};
    }
}

static NSArray *members(MTLStructType *structure) {
    NSMutableArray *result = [NSMutableArray array];
    for (MTLStructMember *member in structure.members) {
        NSMutableDictionary *item = [@{
            @"name": member.name, @"offset": @(member.offset)
        } mutableCopy];
        if (member.dataType == MTLDataTypeArray) {
            MTLArrayType *array = member.arrayType;
            item[@"array_size"] = @(array.arrayLength);
            item[@"array_stride"] = @(array.stride);
            [item addEntriesFromDictionary:typeInfo(array.elementType)];
            if (array.elementType == MTLDataTypeStruct)
                item[@"members"] = members(array.elementStructType);
        } else if (member.dataType == MTLDataTypeStruct) {
            item[@"members"] = members(member.structType);
            item[@"type"] = @"struct";
        } else {
            [item addEntriesFromDictionary:typeInfo(member.dataType)];
        }
        [result addObject:item];
    }
    return result;
}

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static NSArray *arguments(NSArray<MTLArgument *> *args) {
    NSMutableArray *result = [NSMutableArray array];
    for (MTLArgument *arg in args) {
        NSMutableDictionary *item = [@{
            @"name": arg.name, @"index": @(arg.index), @"active": @(arg.active)
        } mutableCopy];
        switch (arg.type) {
            case MTLArgumentTypeBuffer:
                item[@"kind"] = @"buffer";
                item[@"size"] = @(arg.bufferDataSize);
                item[@"alignment"] = @(arg.bufferAlignment);
                item[@"members"] = members(arg.bufferStructType);
                break;
            case MTLArgumentTypeTexture:
                item[@"kind"] = @"texture";
                item[@"texture_type"] = @(arg.textureType);
                break;
            case MTLArgumentTypeSampler:
                item[@"kind"] = @"sampler";
                break;
            default:
                item[@"kind"] = @"unsupported";
                item[@"metal_type"] = @(arg.type);
        }
        [result addObject:item];
    }
    return result;
}
#pragma clang diagnostic pop

static MTLVertexFormat vertexFormat(MTLDataType type) {
    switch (type) {
        case MTLDataTypeFloat: return MTLVertexFormatFloat;
        case MTLDataTypeFloat2: return MTLVertexFormatFloat2;
        case MTLDataTypeFloat3: return MTLVertexFormatFloat3;
        case MTLDataTypeFloat4: return MTLVertexFormatFloat4;
        case MTLDataTypeUInt: return MTLVertexFormatUInt;
        case MTLDataTypeUInt2: return MTLVertexFormatUInt2;
        case MTLDataTypeUInt3: return MTLVertexFormatUInt3;
        case MTLDataTypeUInt4: return MTLVertexFormatUInt4;
        case MTLDataTypeInt: return MTLVertexFormatInt;
        case MTLDataTypeInt2: return MTLVertexFormatInt2;
        case MTLDataTypeInt3: return MTLVertexFormatInt3;
        case MTLDataTypeInt4: return MTLVertexFormatInt4;
        default: return MTLVertexFormatInvalid;
    }
}

static char *dupString(NSString *s) {
    const char *u = s.UTF8String ?: "";
    return strdup(u);
}

static char *toJSON(NSDictionary *report) {
    NSError *error = nil;
    NSData *json = [NSJSONSerialization dataWithJSONObject:report options:0 error:&error];
    if (!json) {
        json = [NSJSONSerialization dataWithJSONObject:@{@"linked": @NO, @"error": error.localizedDescription ?: @"unknown"}
                                               options:0 error:nil];
    }
    char *out = malloc(json.length + 1);
    memcpy(out, json.bytes, json.length);
    out[json.length] = 0;
    return out;
}

static char *fail(NSString *message) {
    return toJSON(@{@"linked": @NO, @"error": message ?: @"unknown"});
}

void *dx2metal_device_new(void) {
    @autoreleasepool {
        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        return device ? (__bridge_retained void *)device : NULL;
    }
}

void dx2metal_release(void *obj) {
    if (obj) CFRelease(obj);
}

void *dx2metal_library_new(void *dev, const char *source, char **err) {
    @autoreleasepool {
        *err = NULL;
        id<MTLDevice> device = (__bridge id<MTLDevice>)dev;
        NSString *text = [NSString stringWithUTF8String:source];
        if (!text) { *err = strdup("MSL 不是合法 UTF-8"); return NULL; }
        MTLCompileOptions *options = [MTLCompileOptions new];
        options.languageVersion = MTLLanguageVersion2_1;
        NSError *error = nil;
        id<MTLLibrary> lib = [device newLibraryWithSource:text options:options error:&error];
        if (!lib) { *err = dupString(error.localizedDescription ?: @"unknown"); return NULL; }
        return (__bridge_retained void *)lib;
    }
}

char *dx2metal_reflect_pair(void *dev, void *vlib, void *flib) {
    @autoreleasepool {
        id<MTLDevice> device = (__bridge id<MTLDevice>)dev;
        id<MTLLibrary> vl = (__bridge id<MTLLibrary>)vlib;
        id<MTLLibrary> fl = (__bridge id<MTLLibrary>)flib;
        NSError *error = nil;
        id<MTLFunction> vs = [vl newFunctionWithName:@"xlatMtlMain"];
        id<MTLFunction> fs = [fl newFunctionWithName:@"xlatMtlMain"];
        MTLRenderPipelineDescriptor *desc = [MTLRenderPipelineDescriptor new];
        desc.vertexFunction = vs;
        desc.fragmentFunction = fs;
        desc.colorAttachments[0].pixelFormat = MTLPixelFormatBGRA8Unorm;
        // URP CopyDepth 等 pass 会写片元深度；片元函数写 [[depth(...)]] 而描述符没有深度附件格式时 Metal 拒绝反射。
        desc.depthAttachmentPixelFormat = MTLPixelFormatDepth32Float;
        MTLVertexDescriptor *vd = [MTLVertexDescriptor vertexDescriptor];
        NSMutableArray *inputs = [NSMutableArray array];
        NSUInteger offset = 0;
        for (MTLAttribute *attr in vs.stageInputAttributes) {
            if (!attr.active) continue;
            MTLVertexFormat format = vertexFormat(attr.attributeType);
            if (format == MTLVertexFormatInvalid) {
                return fail([@"Unsupported vertex format for " stringByAppendingString:attr.name]);
            }
            vd.attributes[attr.attributeIndex].format = format;
            vd.attributes[attr.attributeIndex].offset = offset;
            vd.attributes[attr.attributeIndex].bufferIndex = 30;
            offset += 16;
            NSMutableDictionary *input = [typeInfo(attr.attributeType) mutableCopy];
            input[@"name"] = attr.name;
            input[@"index"] = @(attr.attributeIndex);
            [inputs addObject:input];
        }
        vd.layouts[30].stride = MAX(offset, 16);
        desc.vertexDescriptor = vd;
        MTLRenderPipelineReflection *reflection = nil;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
        MTLPipelineOption opts = MTLPipelineOptionArgumentInfo | MTLPipelineOptionBufferTypeInfo;
        id<MTLRenderPipelineState> state = [device newRenderPipelineStateWithDescriptor:desc
            options:opts reflection:&reflection error:&error];
        if (!state && [error.localizedDescription containsString:@"enough components"]) {
            // 单通道目标（例如 R8/R16F 的 CoC）：换 1 通道格式重试。
            desc.colorAttachments[0].pixelFormat = MTLPixelFormatR16Float;
            error = nil;
            state = [device newRenderPipelineStateWithDescriptor:desc
                options:opts reflection:&reflection error:&error];
        }
        if (!state) {
            return fail(error.localizedDescription);
        }
        NSDictionary *report = @{
            @"device": device.name, @"metal_language": @"2.1",
            @"linked": @YES, @"vertex_inputs": inputs,
            @"vertex": arguments(reflection.vertexArguments),
            @"fragment": arguments(reflection.fragmentArguments)
        };
#pragma clang diagnostic pop
        return toJSON(report);
    }
}
