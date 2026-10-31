import AppKit

// Иконка Mac из готовой квадратной иконки iOS. macOS, в отличие от iOS,
// маску не накладывает: скругление, поля и тень рисует само приложение.
// Сетка Apple: плашка 824×824 в холсте 1024, радиус скругления ~185.
//   swift Scripts/make-mac-icon.swift <icon-1024.png> <каталог AppIcon.appiconset>

let source = NSImage(contentsOfFile: CommandLine.arguments[1])!
let outDir = URL(fileURLWithPath: CommandLine.arguments[2])

func render(_ side: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: side, pixelsHigh: side,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let k = CGFloat(side) / 1024
    let plate = NSRect(x: 100 * k, y: 100 * k, width: 824 * k, height: 824 * k)
    let path = NSBezierPath(roundedRect: plate, xRadius: 185 * k, yRadius: 185 * k)

    let shadow = NSShadow()
    shadow.shadowColor = .black.withAlphaComponent(0.3)
    shadow.shadowOffset = NSSize(width: 0, height: -10 * k)
    shadow.shadowBlurRadius = 20 * k
    NSGraphicsContext.saveGraphicsState()
    shadow.set()
    NSColor.white.setFill()
    path.fill()
    NSGraphicsContext.restoreGraphicsState()

    path.addClip()
    source.draw(in: plate)
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

var images: [String] = [#"{ "filename" : "icon-1024.png", "idiom" : "universal", "platform" : "ios", "size" : "1024x1024" }"#]
for size in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = "mac-\(size)@\(scale)x.png"
        try! render(size * scale).write(to: outDir.appendingPathComponent(name))
        images.append(#"{ "filename" : "\#(name)", "idiom" : "mac", "scale" : "\#(scale)x", "size" : "\#(size)x\#(size)" }"#)
    }
}
let json = "{\n  \"images\" : [\n    " + images.joined(separator: ",\n    ")
    + "\n  ],\n  \"info\" : { \"author\" : \"xcode\", \"version\" : 1 }\n}\n"
try! json.write(to: outDir.appendingPathComponent("Contents.json"), atomically: true, encoding: .utf8)
print("готово: \(images.count - 1) размеров для Mac")
