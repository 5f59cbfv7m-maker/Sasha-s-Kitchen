import SwiftUI

/// Заглушка вкладки «Магазин рецептов».
///
/// Бэкенд магазина живёт в `server/` и разрабатывается в этой же ветке, но
/// приложение к нему **намеренно не ходит**: пока магазин не достроен и не
/// прошёл модерацию, вкладка показывает «Скоро». Здесь нет ни сети, ни
/// моделей, ни ссылок на сервер — подключать их будем одним осознанным шагом,
/// когда магазин будет готов целиком.
struct StoreComingSoonView: View {

    /// Что появится, когда магазин откроется. Список держим здесь, а не в
    /// разметке, чтобы правка текста не задевала вёрстку.
    private static let features: [(symbol: String, title: String, detail: String)] = [
        ("play.rectangle.on.rectangle",
         "Витрина с видео",
         "Рецепты карточками: видео или фото, короткое описание, автор."),
        ("line.3.horizontal.decrease.circle",
         "Умные фильтры",
         "По времени, КБЖУ, кухне и диете. И «могу приготовить сейчас» — из того, что уже лежит в холодильнике."),
        ("square.and.arrow.down",
         "Забрать рецепт себе",
         "Рецепт добавится в «Рецепты», продукты — в каталог, а чего не хватает — сразу в «Для покупки»."),
    ]

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 28) {
                    header
                    VStack(alignment: .leading, spacing: 18) {
                        ForEach(Self.features, id: \.symbol) { feature in
                            row(symbol: feature.symbol,
                                title: feature.title,
                                detail: feature.detail)
                        }
                    }
                    .frame(maxWidth: 560, alignment: .leading)
                }
                .padding()
                .frame(maxWidth: .infinity)
            }
            .navigationTitle("Магазин рецептов")
        }
    }

    private var header: some View {
        VStack(spacing: 12) {
            Image(systemName: "storefront")
                .font(.system(size: 54, weight: .light))
                .foregroundStyle(.tint)
                .padding(.top, 24)

            Text("Скоро")
                .font(.largeTitle.weight(.semibold))

            Text("Здесь появятся рецепты от других людей — с фотографиями, видео и составом, который можно забрать себе одним нажатием.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 520)
        }
    }

    private func row(symbol: String, title: String, detail: String) -> some View {
        HStack(alignment: .top, spacing: 14) {
            Image(systemName: symbol)
                .font(.title3)
                .foregroundStyle(.tint)
                .frame(width: 32)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.headline)
                Text(detail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

#Preview {
    StoreComingSoonView()
}
