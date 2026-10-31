#!/usr/bin/env swift
// Скриншоты для App Store с подписью: снимок из docs/store-raw/<язык>/<устройство>
// (Scripts/store-screenshots.sh) на зелёном фоне приложения, над ним — заголовок
// и строка пояснения на языке страницы.
//
//   swift Scripts/frame-screenshots.swift            # все языки → docs/store/<язык>/<устройство>/
//   swift Scripts/frame-screenshots.swift ja ko      # только эти
//
// Размер — тот же, что у исходника (его и ждёт App Store Connect). Часы
// идут как есть: подпись на экране 422×514 не читается.
import AppKit

let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let raw = root.appending(path: "docs/store-raw")
let out = root.appending(path: "docs/store")

typealias Caption = (title: String, subtitle: String)
let shots = ["1-fridge", "2-shopping", "3-recipe", "4-recipes", "5-log"]

let captions: [String: [Caption]] = [
    "ru": [("Всё о холодильнике", "Что кончается и что скоро испортится — с одного взгляда"),
           ("Покупайте вместе", "Поделитесь списком: отмеченное близкими пополнит холодильник"),
           ("Готовьте из того, что есть", "Граммы и КБЖУ на любое число порций"),
           ("40 рецептов для начала", "Те, что можно приготовить прямо сейчас, — первыми"),
           ("Журнал готовки", "Калории, белки, жиры и углеводы каждого блюда")],
    "en": [("Know your fridge", "What’s running low and what expires soon, at a glance"),
           ("Shop together", "Share the list: what they tick off tops up your fridge"),
           ("Cook from what you have", "Grams and nutrition for any number of servings"),
           ("40 recipes to start", "The ones you can cook right now come first"),
           ("Your cooking log", "Calories, protein, fat and carbs for every dish")],
    "de": [("Dein Kühlschrank im Blick", "Was knapp wird und was bald abläuft – auf einen Blick"),
           ("Gemeinsam einkaufen", "Teile die Liste: Was andere abhaken, füllt deinen Kühlschrank"),
           ("Koche mit dem, was da ist", "Gramm und Nährwerte für jede Portionenzahl"),
           ("40 Rezepte zum Start", "Was du jetzt kochen kannst, steht ganz oben"),
           ("Dein Kochprotokoll", "Kalorien, Eiweiß, Fett und Kohlenhydrate jedes Gerichts")],
    "fr": [("Votre frigo d’un coup d’œil", "Ce qui manque et ce qui périme bientôt, en un regard"),
           ("Faites les courses à deux", "Partagez la liste : ce qu’ils cochent remplit votre frigo"),
           ("Cuisinez avec ce que vous avez", "Grammes et valeurs nutritionnelles pour toutes les portions"),
           ("40 recettes pour commencer", "Celles que vous pouvez cuisiner tout de suite en premier"),
           ("Votre journal de cuisine", "Calories, protéines, lipides et glucides de chaque plat")],
    "es": [("Tu nevera de un vistazo", "Lo que se acaba y lo que caduca pronto, de un vistazo"),
           ("Compra en compañía", "Comparte la lista: lo que marquen llena tu nevera"),
           ("Cocina con lo que tienes", "Gramos y nutrientes para cualquier número de raciones"),
           ("40 recetas para empezar", "Primero, las que puedes cocinar ahora mismo"),
           ("Tu diario de cocina", "Calorías, proteínas, grasas y carbohidratos de cada plato")],
    "it": [("Il tuo frigo a colpo d’occhio", "Cosa sta finendo e cosa scade presto, in un attimo"),
           ("Fate la spesa insieme", "Condividi la lista: ciò che spuntano riempie il tuo frigo"),
           ("Cucina con quello che hai", "Grammi e valori nutrizionali per qualsiasi numero di porzioni"),
           ("40 ricette per iniziare", "Prima quelle che puoi cucinare subito"),
           ("Il tuo diario di cucina", "Calorie, proteine, grassi e carboidrati di ogni piatto")],
    "pt-BR": [("Sua geladeira num relance", "O que está acabando e o que vence logo, num relance"),
              ("Façam as compras juntos", "Compartilhe a lista: o que marcarem abastece sua geladeira"),
              ("Cozinhe com o que tem", "Gramas e nutrientes para qualquer número de porções"),
              ("40 receitas para começar", "As que dá para fazer agora aparecem primeiro"),
              ("Seu diário de cozinha", "Calorias, proteínas, gorduras e carboidratos de cada prato")],
    "ja": [("冷蔵庫がひと目でわかる", "残りわずかなもの、期限が近いものがすぐわかる"),
           ("買い物はいっしょに", "リストを共有。チェックされた分が冷蔵庫に補充されます"),
           ("あるもので料理", "何人分でも、グラムと栄養素を自動で計算"),
           ("40のレシピからスタート", "今すぐ作れるレシピが先頭に"),
           ("料理の記録", "どの料理もカロリー、たんぱく質、脂質、炭水化物まで")],
    "ko": [("냉장고를 한눈에", "떨어져 가는 것과 곧 상하는 것을 한눈에"),
           ("장보기는 함께", "목록을 공유하세요. 체크한 만큼 냉장고가 채워집니다"),
           ("있는 재료로 요리", "몇 인분이든 그램과 영양 성분을 계산"),
           ("시작용 레시피 40가지", "지금 바로 만들 수 있는 레시피가 맨 앞에"),
           ("요리 기록", "요리마다 칼로리, 단백질, 지방, 탄수화물까지")],
    "zh-Hans": [("冰箱一目了然", "快用完的、快过期的，一眼就知道"),
                ("一起去采购", "共享清单：对方勾选的东西会补进你的冰箱"),
                ("用现有食材做饭", "任意份数都能算出克数和营养"),
                ("40 道菜谱开始", "现在就能做的排在最前面"),
                ("烹饪记录", "每道菜的热量、蛋白质、脂肪和碳水")],
    "th": [("ดูตู้เย็นได้ในพริบตา", "ของที่ใกล้หมดและใกล้หมดอายุ เห็นได้ทันที"),
           ("ช่วยกันซื้อของ", "แชร์รายการ: ของที่อีกฝ่ายติ๊กจะเติมเข้าตู้เย็นของคุณ"),
           ("ทำอาหารจากของที่มี", "คำนวณกรัมและสารอาหารได้ทุกจำนวนที่"),
           ("เริ่มต้นด้วย 40 สูตร", "สูตรที่ทำได้ทันทีขึ้นก่อน"),
           ("บันทึกการทำอาหาร", "แคลอรี โปรตีน ไขมัน และคาร์บของทุกเมนู")],
]

// Зелёный — акцент приложения (AccentColor), ниже чуть темнее.
let top = NSColor(srgbRed: 0.145, green: 0.640, blue: 0.380, alpha: 1)
let bottom = NSColor(srgbRed: 0.075, green: 0.430, blue: 0.250, alpha: 1)

func font(_ size: CGFloat, _ weight: NSFont.Weight) -> NSFont {
    let base = NSFont.systemFont(ofSize: size, weight: weight)
    return NSFont(descriptor: base.fontDescriptor.withDesign(.rounded) ?? base.fontDescriptor, size: size) ?? base
}

func draw(_ text: String, font: NSFont, color: NSColor, in rect: NSRect) -> CGFloat {
    let style = NSMutableParagraphStyle()
    style.alignment = .center
    style.lineBreakMode = .byWordWrapping
    style.lineHeightMultiple = 1.05
    let string = NSAttributedString(string: text, attributes: [.font: font, .foregroundColor: color, .paragraphStyle: style])
    let height = string.boundingRect(with: rect.size, options: [.usesLineFragmentOrigin, .usesFontLeading]).height
    string.draw(with: NSRect(x: rect.minX, y: rect.maxY - height, width: rect.width, height: height),
                options: [.usesLineFragmentOrigin, .usesFontLeading])
    return height
}

func frame(_ source: URL, caption: Caption, to target: URL, isMac: Bool) throws {
    guard let shot = NSImage(contentsOf: source),
          let rep = shot.representations.first else { throw CocoaError(.fileReadCorruptFile) }
    let width = CGFloat(rep.pixelsWide), height = CGFloat(rep.pixelsHigh)
    let canvas = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(width), pixelsHigh: Int(height),
                                  bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 32)!
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: canvas)
    NSGradient(starting: top, ending: bottom)!.draw(in: NSRect(x: 0, y: 0, width: width, height: height), angle: -90)

    // Текст сверху, снимок под ним уходит за нижний край — как устройство в руке.
    let margin = width * 0.07
    let titleSize = isMac ? height * 0.065 : width * 0.075
    let textTop = height - (isMac ? height * 0.07 : height * 0.055)
    let textRect = NSRect(x: margin, y: 0, width: width - margin * 2, height: textTop)
    let titleHeight = draw(caption.title, font: font(titleSize, .heavy), color: .white, in: textRect)
    let subRect = NSRect(x: margin, y: 0, width: width - margin * 2, height: textTop - titleHeight - titleSize * 0.3)
    let subHeight = draw(caption.subtitle, font: font(titleSize * 0.52, .semibold),
                         color: NSColor(white: 1, alpha: 0.85), in: subRect)
    let textBottom = textTop - titleHeight - titleSize * 0.3 - subHeight

    let scale: CGFloat = isMac ? 0.80 : 0.84
    let shotWidth = width * scale, shotHeight = height * scale
    let shotTop = textBottom - height * 0.04
    let shotRect = NSRect(x: (width - shotWidth) / 2, y: shotTop - shotHeight, width: shotWidth, height: shotHeight)
    let radius = shotWidth * (isMac ? 0.015 : 0.085)
    let bezel = shotWidth * (isMac ? 0.006 : 0.022)

    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor(white: 0, alpha: 0.35)
    shadow.shadowBlurRadius = width * 0.03
    shadow.shadowOffset = NSSize(width: 0, height: -width * 0.01)
    shadow.set()
    NSColor(white: 0.08, alpha: 1).setFill()
    NSBezierPath(roundedRect: shotRect.insetBy(dx: -bezel, dy: -bezel),
                 xRadius: radius + bezel, yRadius: radius + bezel).fill()
    NSGraphicsContext.restoreGraphicsState()

    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(roundedRect: shotRect, xRadius: radius, yRadius: radius).addClip()
    shot.draw(in: shotRect)
    NSGraphicsContext.restoreGraphicsState()

    NSGraphicsContext.current?.flushGraphics()
    try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
    try canvas.representation(using: .png, properties: [:])!.write(to: target)
}

let languages = CommandLine.arguments.count > 1 ? Array(CommandLine.arguments.dropFirst()) : captions.keys.sorted()
for language in languages {
    guard let lines = captions[language] else { print("Нет подписей для \(language)"); exit(1) }
    for device in ["iphone", "ipad", "mac"] {
        for (index, name) in shots.enumerated() {
            let source = raw.appending(path: "\(language)/\(device)/\(name).png")
            guard FileManager.default.fileExists(atPath: source.path) else { continue }
            try frame(source, caption: lines[index], to: out.appending(path: "\(language)/\(device)/\(name).png"),
                      isMac: device == "mac")
        }
    }
    let watch = raw.appending(path: "\(language)/watch")
    for file in (try? FileManager.default.contentsOfDirectory(at: watch, includingPropertiesForKeys: nil)) ?? [] {
        let target = out.appending(path: "\(language)/watch/\(file.lastPathComponent)")
        try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? FileManager.default.removeItem(at: target)
        try FileManager.default.copyItem(at: file, to: target)
    }
    print(language)
}
