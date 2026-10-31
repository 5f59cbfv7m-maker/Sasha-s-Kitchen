import AVFoundation
import SwiftData
import SwiftUI

/// Вкладка «Магазин»: рецепты других людей полками и лента коротких видео.
struct StoreView: View {
    private enum Section: Hashable { case recipes, shorts }
    @State private var section = Section.recipes

    var body: some View {
        NavigationStack {
            Group {
                switch section {
                case .recipes: StoreShelvesView()
                case .shorts: ShortsFeedView()
                }
            }
            // Над роликом заголовок только мешает.
            .navigationTitle(section == .recipes ? Text("Магазин") : Text(verbatim: ""))
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Picker("Раздел", selection: $section) {
                        Text("Рецепты").tag(Section.recipes)
                        Text("Ролики").tag(Section.shorts)
                    }
                    .pickerStyle(.segmented)
                    .frame(maxWidth: 260)
                }
            }
        }
    }
}

/// Загрузка с понятной ошибкой и повтором — общая для экранов магазина.
private struct Loading<Value, Content: View>: View {
    let load: () async throws -> Value
    @ViewBuilder let content: (Value) -> Content
    @State private var value: Value?
    @State private var error: String?

    var body: some View {
        Group {
            if let value {
                content(value)
            } else if let error {
                ContentUnavailableView {
                    Label("Магазин недоступен", systemImage: "wifi.exclamationmark")
                } description: {
                    Text(error)
                } actions: {
                    Button("Повторить") { Task { await run() } }
                }
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task { if value == nil { await run() } }
    }

    private func run() async {
        error = nil
        do { value = try await load() } catch { self.error = error.localizedDescription }
    }
}

// MARK: Полки

private struct StoreShelvesView: View {
    var body: some View {
        Loading(load: { try await StoreAPI.get("v1/storefront") as StoreFront }) { front in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 24) {
                    if front.shelves.isEmpty {
                        ContentUnavailableView("Здесь пока пусто", systemImage: "storefront")
                    }
                    ForEach(front.shelves) { shelf in
                        Text(shelf.title).font(.title2.bold()).padding(.horizontal)
                        ScrollView(.horizontal, showsIndicators: false) {
                            LazyHStack(spacing: 14) {
                                ForEach(shelf.items) { card in
                                    NavigationLink(value: card) { StoreCardTile(card: card) }
                                        .buttonStyle(.plain)
                                }
                            }
                            .padding(.horizontal)
                        }
                    }
                }
                .padding(.vertical)
            }
            .navigationDestination(for: StoreCard.self) { StoreRecipeView(card: $0) }
        }
    }
}

private struct StoreCardTile: View {
    let card: StoreCard

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            StoreImage(url: card.media?.imageURL)
                .frame(width: 220, height: 150)
                .clipShape(.rect(cornerRadius: 14))
            Text(card.title).font(.headline).lineLimit(2)
            Text("\(Fmt.minutes(card.cookTimeMinutes)) · \(Fmt.kcal(card.nutritionPerServing.kcal))")
                .font(.caption).foregroundStyle(.secondary)
            Text(verbatim: "@" + card.author.handle).font(.caption).foregroundStyle(.tint)
        }
        .frame(width: 220, alignment: .leading)
    }
}

private struct StoreImage: View {
    let url: URL?

    var body: some View {
        AsyncImage(url: url) { image in
            image.resizable().scaledToFill()
        } placeholder: {
            Rectangle().fill(.quaternary).overlay { Image(systemName: "fork.knife").foregroundStyle(.secondary) }
        }
    }
}

// MARK: Рецепт

private struct StoreRecipeView: View {
    @Environment(\.modelContext) private var context
    let card: StoreCard
    @State private var imported = false
    @State private var importing = false
    @State private var error: String?

    var body: some View {
        Loading(load: { try await StoreAPI.get("v1/recipes/\(card.id)") as StoreRecipeDetail }) { detail in
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    StoreImage(url: detail.media?.imageURL)
                        .frame(height: 260).frame(maxWidth: .infinity)
                        .clipShape(.rect(cornerRadius: 18))
                    Text(detail.title).font(.largeTitle.bold())
                    Text(verbatim: "\(detail.author.name) · @\(detail.author.handle)").foregroundStyle(.secondary)
                    if let summary = detail.summary, !summary.isEmpty { Text(summary) }
                    Text("\(Fmt.minutes(detail.cookTimeMinutes + detail.prepTimeMinutes)) · \(Fmt.kcal(detail.nutritionPerServing.kcal))")
                        .font(.subheadline).foregroundStyle(.secondary)

                    importButton(detail)

                    if let ingredients = detail.ingredients, !ingredients.isEmpty {
                        Text("Ингредиенты").font(.title3.bold())
                        let catalog = StoreImport.catalog(in: context)
                        ForEach(ingredients, id: \.self) { item in
                            let have = StoreImport.match(item.name, in: catalog)
                            HStack {
                                Image(systemName: have == nil ? "circle" : "checkmark.circle.fill")
                                    .foregroundStyle(have == nil ? Color.secondary : Color.green)
                                Text(have?.name ?? item.name)
                                Spacer()
                                Text(Fmt.amount(item.amount, unit: item.unit)).foregroundStyle(.secondary)
                            }
                        }
                    }
                    if let steps = detail.steps, !steps.isEmpty {
                        Text("Шаги приготовления").font(.title3.bold())
                        ForEach(Array(steps.enumerated()), id: \.offset) { index, step in
                            Text(verbatim: "\(index + 1). \(step.text)")
                        }
                    }
                }
                .padding()
                .frame(maxWidth: 760)
                .frame(maxWidth: .infinity)
            }
            .navigationBarTitleDisplayMode(.inline)
        }
        .onAppear { imported = StoreImport.existing(card.id, in: context) != nil }
    }

    @ViewBuilder
    private func importButton(_ detail: StoreRecipeDetail) -> some View {
        if imported {
            Label("Рецепт в ваших рецептах", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
        } else if detail.canImport {
            Button {
                Task { await take() }
            } label: {
                Label(importing ? "Забираем…" : "Забрать себе", systemImage: "square.and.arrow.down")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .disabled(importing)
            if let error { Text(error).font(.footnote).foregroundStyle(.orange) }
        }
    }

    private func take() async {
        importing = true
        defer { importing = false }
        do {
            let bundle: StoreBundle = try await StoreAPI.get("v1/recipes/\(card.id)/bundle")
            var photo: Data?
            if let url = bundle.recipe.photoUrl { photo = try? await URLSession.shared.data(from: url).0 }
            StoreImport.apply(bundle, photo: photo, to: context)
            Feedback.shared.play(.added)
            imported = true
        } catch {
            Feedback.shared.play(.warning)
            self.error = error.localizedDescription
        }
    }
}

// MARK: Ролики

private struct ShortsFeedView: View {
    @State private var posts: [StorePost] = []
    @State private var cursor: String?
    @State private var hasMore = true
    @State private var active: String?
    @State private var error: String?

    var body: some View {
        ScrollView(.vertical) {
            LazyVStack(spacing: 0) {
                ForEach(posts) { post in
                    // Ролик — ровно на экран, как в Shorts: листается по одному.
                    ShortPage(post: post, isActive: active == post.id)
                        .containerRelativeFrame([.horizontal, .vertical])
                        .onAppear { if post.id == posts.last?.id { Task { await loadMore() } } }
                }
            }
            .scrollTargetLayout()
        }
        .scrollTargetBehavior(.paging)
        .scrollPosition(id: $active)
        .scrollIndicators(.hidden)
        .background(.black)
        .ignoresSafeArea(edges: .bottom)
        .overlay {
            if posts.isEmpty, let error {
                ContentUnavailableView("Магазин недоступен", systemImage: "wifi.exclamationmark",
                                       description: Text(error))
            } else if posts.isEmpty, !hasMore {
                ContentUnavailableView("Роликов пока нет", systemImage: "play.rectangle")
            }
        }
        .task { if posts.isEmpty { await loadMore() } }
        .onChange(of: active) { _, id in
            // Просмотр засчитывается, когда ролик оказался на экране.
            if let id { Task { await StoreAPI.fire("POST", "v1/posts/\(id)/view") } }
        }
    }

    private func loadMore() async {
        guard hasMore else { return }
        do {
            let page: StorePage<StorePost> = try await StoreAPI.get(
                "v1/shorts", query: cursor.map { [URLQueryItem(name: "cursor", value: $0)] } ?? [])
            posts += page.items.filter { new in !posts.contains { $0.id == new.id } }
            cursor = page.nextCursor
            hasMore = page.hasMore
            if active == nil { active = posts.first?.id }
        } catch {
            self.error = error.localizedDescription
            hasMore = false
        }
    }
}

private struct ShortPage: View {
    let post: StorePost
    let isActive: Bool
    @State private var player: AVPlayer?

    var body: some View {
        // Постер и плеер — фоном: картинка с scaledToFill иначе растягивает
        // страницу, и подпись уезжает за край экрана.
        Color.black
            .overlay { StoreImage(url: post.media?.posterUrl) }
            .overlay { if let player { PlayerLayer(player: player) } }
            .overlay(alignment: .bottomLeading) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(post.title).font(.title3.bold())
                    Text(verbatim: "@" + post.author.handle).font(.subheadline)
                    HStack(spacing: 16) {
                        Label("\(post.likeCount)", systemImage: "heart")
                        Label("\(post.viewCount)", systemImage: "eye")
                    }
                    .font(.footnote)
                }
                .foregroundStyle(.white)
                .shadow(radius: 4)
                .padding()
                .padding(.bottom, 24)
            }
            .clipped()
            .contentShape(.rect)
            // Нажатие — пауза и продолжение, как в любой ленте роликов.
            .onTapGesture {
                guard let player else { return }
                if player.timeControlStatus == .paused { player.play() } else { player.pause() }
            }
            .onChange(of: isActive, initial: true) { _, active in
                if active {
                    if player == nil, let url = post.media?.url { player = AVPlayer(url: url) }
                    player?.play()
                } else {
                    player?.pause()
                }
            }
            // Шортс крутится по кругу, пока на экране.
            .onReceive(NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification)) { note in
                guard let item = note.object as? AVPlayerItem, item == player?.currentItem else { return }
                player?.seek(to: .zero)
                if isActive { player?.play() }
            }
    }
}

/// Видео на весь кадр без системных кнопок: у шортса управление — касание.
private struct PlayerLayer: UIViewRepresentable {
    let player: AVPlayer

    final class View: UIView {
        override class var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }

    func makeUIView(context: Context) -> View {
        let view = View()
        view.playerLayer.videoGravity = .resizeAspectFill
        view.playerLayer.player = player
        return view
    }

    func updateUIView(_ view: View, context: Context) {
        view.playerLayer.player = player
    }
}
