import AppKit
import CoreImage
import CoreGraphics

// Иллюстрация нарисована как «готовая иконка»: скругление радиусом 274 px и
// зелёная рамка уже зашиты в пиксели, снаружи белое. iOS накладывает свою
// маску поверх, поэтому скругление надо срезать — иначе выйдет скругление
// внутри скругления и белые уголки по краям.
//
// Обрезаем ровно настолько, чтобы угол кадра ушёл внутрь дуги:
// точка (inset, inset) должна попасть в круг радиуса r с центром (r, r).

let inPath = CommandLine.arguments[1]
let outPath = CommandLine.arguments[2]
let radius = Double(CommandLine.arguments.count > 3 ? Int(CommandLine.arguments[3])! : 274)

guard let src = NSImage(contentsOfFile: inPath),
      let cg = src.cgImage(forProposedRect: nil, context: nil, hints: nil) else { exit(1) }

let w = Double(cg.width), h = Double(cg.height)
// r(1 − 1/√2) — минимум; +12 px запаса на сглаживание дуги.
let inset = (radius * (1 - 1 / 2.0.squareRoot())).rounded(.up) + 12
let side = min(w, h) - inset * 2
print("исходник \(Int(w))x\(Int(h)), срез по \(Int(inset)) px → кадр \(Int(side))x\(Int(side))")

let cropped = CIImage(cgImage: cg).cropped(
    to: CGRect(x: inset, y: inset, width: side, height: side))
    .transformed(by: CGAffineTransform(translationX: -inset, y: -inset))

let graded = cropped.applyingFilter("CIColorControls", parameters: [
    kCIInputSaturationKey: 1.08,
    kCIInputContrastKey: 1.03,
])
let sharpened = graded.applyingFilter("CISharpenLuminance", parameters: [
    kCIInputSharpnessKey: 0.30,
])

let target = 1024.0
let scaled = sharpened.transformed(
    by: CGAffineTransform(scaleX: target / side, y: target / side))

let space = CGColorSpaceCreateDeviceRGB()
let ciContext = CIContext(options: [.outputColorSpace: space, .workingColorSpace: space])
guard let final = ciContext.createCGImage(
    scaled, from: CGRect(x: 0, y: 0, width: target, height: target)) else { exit(1) }

// Контроль: в углах не должно остаться ничего белого.
let check = UnsafeMutablePointer<UInt8>.allocate(capacity: Int(target * target) * 4)
defer { check.deallocate() }
let ctx = CGContext(data: check, width: Int(target), height: Int(target), bitsPerComponent: 8,
                    bytesPerRow: Int(target) * 4, space: space,
                    bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
ctx.draw(final, in: CGRect(x: 0, y: 0, width: target, height: target))
var white = 0
for y in 0..<Int(target) {
    for x in 0..<Int(target) {
        let i = (y * Int(target) + x) * 4
        if check[i] > 238 && check[i+1] > 238 && check[i+2] > 238 { white += 1 }
    }
}
print("почти белых пикселей осталось: \(white)")

let rep = NSBitmapImageRep(cgImage: final)
rep.size = NSSize(width: target, height: target)
try rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: outPath))
print("иконка: \(outPath) — 1024x1024")
