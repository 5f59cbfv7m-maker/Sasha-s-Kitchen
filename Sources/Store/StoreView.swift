import AVFoundation
import SwiftData
import SwiftUI

/// Вкладка «Магазин»: рецепты других людей полками и лента коротких видео.
struct StoreView: View {
    private enum Section: Hashable { case recipes, shorts }
    /// `-storeSection shorts` — сразу ролики (скриншоты и проверка).
    @State private var section = UserDefaults.standard.string(forKey: "storeSection") == "shorts"
        ? Section.shorts : Section.recipes
    @State private var showsAccount = false

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
            // Ролик уходит под панель, как в Shorts: фона у неё нет.
            .toolbarBackground(section == .shorts ? .hidden : .automatic, for: .navigationBar)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Picker("Раздел", selection: $section) {
                        Text("Рецепты").tag(Section.recipes)
                        Text("Ролики").tag(Section.shorts)
                    }
                    .pickerStyle(.segmented)
                    .frame(maxWidth: 260)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showsAccount = true } label: {
                        Label("Аккаунт", systemImage: StoreAccount.shared.user == nil ? "person.crop.circle" : "person.crop.circle.fill")
                    }
                }
            }
            .sheet(isPresented: $showsAccount) { StoreAccountView() }
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
    @State private var showsComments = false
    @Environment(\.dismiss) private var dismiss

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
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { showsComments = true } label: { Label("Комментарии", systemImage: "bubble.left") }
            }
            ToolbarItem(placement: .topBarTrailing) {
                // Заблокированный автор пропадает — уходим с его рецепта.
                StoreModerationMenu(targetType: "recipe", targetID: card.id, author: card.author) { dismiss() }
            }
        }
        .sheet(isPresented: $showsComments) { StoreCommentsView(kind: "recipes", id: card.id) }
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
    /// Лента на экране: ушли на «Рецепты» или другую вкладку — звук стихает.
    @State private var isVisible = false

    var body: some View {
        // Ролик — на весь экран, под панелями, как в Shorts; подписи и кнопки
        // — внутри безопасной зоны, иначе их закрывают меню и «чёлка».
        GeometryReader { proxy in
            let insets = proxy.safeAreaInsets
            let page = CGSize(width: proxy.size.width + insets.leading + insets.trailing,
                              height: proxy.size.height + insets.top + insets.bottom)
            ScrollView(.vertical) {
                LazyVStack(spacing: 0) {
                    ForEach(posts) { post in
                        ShortPage(post: post, isActive: isVisible && active == post.id, insets: insets) {
                            posts.removeAll { $0.author.id == post.author.id }
                        }
                        .frame(width: page.width, height: page.height)
                        .onAppear { if post.id == posts.last?.id { Task { await loadMore() } } }
                    }
                }
                .scrollTargetLayout()
            }
            // Один свайп — один ролик, как бы сильно ни листнули.
            .scrollTargetBehavior(.viewAligned(limitBehavior: .always))
            .scrollPosition(id: $active)
            .scrollIndicators(.hidden)
            // Без размытой полосы у краёв: ролик виден целиком.
            .scrollEdgeEffectHidden(true, for: .all)
            .ignoresSafeArea()
        }
        .background(.black)
        .onAppear { isVisible = true }
        .onDisappear { isVisible = false }
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
    let insets: EdgeInsets
    var onBlocked: () -> Void = {}
    @State private var player: AVPlayer?
    @State private var liked: Bool?
    @State private var likeCount: Int?
    @State private var showsComments = false
    @State private var needsAccount = false

    var body: some View {
        // Постер и плеер — фоном: картинка с scaledToFill иначе растягивает
        // страницу, и подпись уезжает за край экрана.
        Color.black
            .overlay { StoreImage(url: post.media?.posterUrl) }
            .overlay { if let player { PlayerLayer(player: player) } }
            // Затемнение снизу: белые подписи читаются на любом кадре.
            .overlay(alignment: .bottom) {
                LinearGradient(colors: [.clear, .black.opacity(0.55)], startPoint: .top, endPoint: .bottom)
                    .frame(height: 260 + insets.bottom)
                    .allowsHitTesting(false)
            }
            .overlay(alignment: .bottomLeading) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(post.title).font(.title3.bold())
                    Text(verbatim: "@" + post.author.handle).font(.subheadline)
                    Label("\(post.viewCount)", systemImage: "eye").font(.footnote)
                }
                .foregroundStyle(.white)
                .shadow(radius: 4)
                .padding()
                .padding(.leading, insets.leading)
                .padding(.trailing, 72 + insets.trailing)
                .padding(.bottom, insets.bottom)
            }
            // Справа, как в любой ленте роликов: лайк, комментарии, «ещё».
            .overlay(alignment: .bottomTrailing) {
                VStack(spacing: 18) {
                    Button { Task { await toggleLike() } } label: {
                        VStack(spacing: 2) {
                            Image(systemName: (liked ?? post.liked ?? false) ? "heart.fill" : "heart").font(.title)
                            Text("\(likeCount ?? post.likeCount)").font(.caption)
                        }
                    }
                    .foregroundStyle((liked ?? post.liked ?? false) ? .red : .white)
                    Button { showsComments = true } label: {
                        VStack(spacing: 2) {
                            Image(systemName: "bubble.right").font(.title)
                            Text("\(post.commentCount)").font(.caption)
                        }
                    }
                    StoreModerationMenu(targetType: "post", targetID: post.id, author: post.author, onBlocked: onBlocked)
                }
                .foregroundStyle(.white)
                .shadow(radius: 4)
                .padding()
                .padding(.trailing, insets.trailing)
                .padding(.bottom, insets.bottom)
            }
            .sheet(isPresented: $showsComments) { StoreCommentsView(kind: "posts", id: post.id) }
            .sheet(isPresented: $needsAccount) { StoreAccountView() }
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
            // Ушли со страницы (другая вкладка, закрыли магазин) — тишина.
            .onDisappear { player?.pause() }
            // Шортс крутится по кругу, пока на экране.
            .onReceive(NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification)) { note in
                guard let item = note.object as? AVPlayerItem, item == player?.currentItem else { return }
                player?.seek(to: .zero)
                if isActive { player?.play() }
            }
    }
}

private struct LikeResponse: Decodable {
    let likeCount: Int
    let liked: Bool
}

extension ShortPage {
    /// Лайк сразу на экране, потом — подтверждение сервера.
    fileprivate func toggleLike() async {
        guard StoreAccount.shared.user != nil else { needsAccount = true; return }
        let wasLiked = liked ?? post.liked ?? false
        liked = !wasLiked
        do {
            let response: LikeResponse = try await StoreAPI.send(wasLiked ? "DELETE" : "PUT", "v1/posts/\(post.id)/like")
            liked = response.liked
            likeCount = response.likeCount
        } catch {
            liked = wasLiked
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
