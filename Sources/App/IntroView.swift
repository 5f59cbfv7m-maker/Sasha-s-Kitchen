import SwiftData
import SwiftUI

/// Заставка при холодном старте: иконки категорий, что лежат в холодильнике,
/// разлетаются салютом и схлопываются в иконку приложения. Тап — пропустить.
/// `-skipIntro YES` в аргументах запуска отключает её (UI-тесты, скриншоты).
struct IntroView: View {
    let onFinish: () -> Void

    @Query private var stock: [StockItem]
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// 0 — пусто, 1 — иконки разлетелись, 2 — собрались в иконку приложения.
    @State private var phase = 0

    static var isSkipped: Bool { UserDefaults.standard.bool(forKey: "skipIntro") }

    private var categories: [String] {
        Array(Set(stock.map(\.category))).sorted { CategoryStyle.rank($0) < CategoryStyle.rank($1) }
    }

    var body: some View {
        GeometryReader { geometry in
            let radius = min(190, min(geometry.size.width, geometry.size.height) * 0.3)
            ZStack {
                Color("LaunchBackground")
                ForEach(Array(categories.enumerated()), id: \.element) { index, category in
                    let angle = Double(index) / Double(categories.count) * 2 * .pi
                    let distance = phase == 1 ? radius : 0
                    Image(systemName: CategoryStyle.symbol(for: category))
                        .font(.system(size: 28))
                        .foregroundStyle(.white)
                        .frame(width: 64, height: 64)
                        .background(CategoryStyle.color(for: category), in: .circle)
                        .scaleEffect(phase == 1 ? 1 : 0.2)
                        .opacity(phase == 1 ? 1 : 0)
                        .offset(x: cos(angle) * distance, y: sin(angle) * distance)
                        .animation(.spring(duration: 0.8, bounce: 0.35).delay(Double(index) * 0.04),
                                   value: phase)
                }
                VStack(spacing: 16) {
                    // Копия иконки приложения (IntroIcon, 512 px): у AppIcon в бандле
                    // только 152 px, а скругление iOS накладывает сам — режем так же.
                    Image("IntroIcon")
                        .resizable()
                        .frame(width: 128, height: 128)
                        .clipShape(.rect(cornerRadius: 128 * 0.2237, style: .continuous))
                        .shadow(color: .black.opacity(0.18), radius: 16, y: 8)
                    Text("Sasha’s Kitchen")
                        .font(.title.weight(.bold))
                }
                .scaleEffect(phase == 2 ? 1 : 0.6)
                .opacity(phase == 2 ? 1 : 0)
            }
            .frame(width: geometry.size.width, height: geometry.size.height)
        }
        .ignoresSafeArea()
        .contentShape(Rectangle())
        .onTapGesture(perform: onFinish)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Заставка Sasha’s Kitchen")
        .accessibilityHint("Коснитесь, чтобы пропустить")
        .accessibilityAddTraits(.isButton)
        .task {
            if reduceMotion {
                withAnimation(.easeOut(duration: 0.4)) { phase = 2 }
                try? await Task.sleep(for: .seconds(1))
                return onFinish()
            }
            try? await Task.sleep(for: .milliseconds(200))
            withAnimation(.spring(duration: 0.9, bounce: 0.25)) { phase = 1 }
            try? await Task.sleep(for: .seconds(1.6))
            withAnimation(.spring(duration: 0.6, bounce: 0.1)) { phase = 2 }
            try? await Task.sleep(for: .seconds(1.1))
            onFinish()
        }
    }
}
