import SwiftUI

/// Авторы фото стартовых рецептов: снимки с Wikimedia Commons под CC0,
/// Public Domain и CC BY — последняя требует указать автора.
struct PhotoCreditsView: View {
    private struct Credit: Decodable, Identifiable {
        let key: String
        let title: String
        let author: String
        let license: String
        let sourceURL: String
        var id: String { key }
    }

    private let credits: [Credit] = Bundle.main.url(forResource: "recipe-photo-credits", withExtension: "json")
        .flatMap { try? Data(contentsOf: $0) }
        .flatMap { try? JSONDecoder().decode([Credit].self, from: $0) } ?? []

    var body: some View {
        List {
            Section {
                ForEach(credits) { credit in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(Bundle.main.localizedString(forKey: credit.key, value: credit.key, table: "Seed"))
                        Text("\(credit.author) · \(credit.license)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        if let url = URL(string: credit.sourceURL) {
                            Link("Wikimedia Commons", destination: url).font(.caption)
                        }
                    }
                }
            } footer: {
                Text("Фото стартовых рецептов — с Wikimedia Commons, под свободными лицензиями. Спасибо авторам!")
            }
        }
        .navigationTitle("Фото рецептов")
    }
}
