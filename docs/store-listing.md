# Тексты для App Store Connect на 11 языках

Те же языки, что и в интерфейсе приложения
(`Shared/Resources/Localizable.xcstrings`). В App Store Connect каждый язык
добавляется отдельно: App Store → Localizations → «+», затем вставить блок
целиком. Языки, которых нет в списке магазина, показываются на английском.

**Name** везде одинаковый: `Sasha’s Kitchen` — имя не переводится, так его
ищут в любом магазине. Если App Store Connect скажет, что имя занято, —
`Sasha’s Kitchen: Fridge & List`.

Лимиты: подзаголовок 30 символов, промо-текст 170, ключевые слова 100
(через запятую, без пробелов вокруг запятых), описание 4000. Все тексты ниже
в них укладываются — проверяет `python3 Scripts/check-store-listing.py`; если
правите, запустите его: App Store Connect обрезает молча. Промо-текст можно
менять без новой версии, описание — только с новой версией.

Ключевые слова не повторяют слов из названия и подзаголовка: App Store сам
складывает слова из всех трёх полей в запросы, повтор только съедает место.

## English (U.S.)

**Subtitle:** Fridge, recipes, shopping list

**Promotional text:** Share the shopping list with your family through iCloud: what they tick off in the store tops up your fridge. With a barcode scanner and Apple Watch app.

**Keywords:** `pantry,grocery,groceries,inventory,meal,planner,nutrition,calories,cooking,barcode,family,expiry`

**Description:**

> Sasha’s Kitchen keeps track of what is in your fridge and what you can cook from it right now. No ads, no subscriptions, no account to create.
>
> Your fridge at a glance
> Every product shows how much is left and how long it keeps. What is running low and what expires soon stands out by itself.
>
> Cook from what you have
> Recipes you can make from your stock come first; the others show exactly what is missing — one tap puts it on the shopping list. Grams, calories, protein, fat and carbs are recalculated for any number of servings.
>
> Cooking deducts what you really used
> Tap Cooked, adjust the grams if you used more or less, and the fridge and the cooking log update themselves.
>
> A shopping list that fills itself
> Whatever runs out lands on the list with a sensible amount to buy. Add anything else by typing it or scanning a barcode — the product is looked up in the open Open Food Facts database. Ticking an item off tops up your fridge.
>
> Shop together
> Invite someone to your list through iCloud. They see it in their own app, tick off what they bought — and your fridge is topped up — and add what you forgot. Your fridge and recipes stay private.
>
> On iPhone, iPad, Mac and Apple Watch
> Everything syncs through your iCloud. On Apple Watch the list is on your wrist: tap an item as it goes into the basket. Send the list to Reminders as a real checklist or share it as text.
>
> 40 recipes and 49 products to start with, 11 languages, light and dark themes. Your data stays on your devices and in your own iCloud.

## Русский — Russian

**Subtitle:** Холодильник, рецепты, покупки

**Promotional text:** Поделитесь списком покупок с семьёй через iCloud: отмеченное в магазине пополнит ваш холодильник. Со сканером штрихкодов и приложением для Apple Watch.

**Keywords:** `продукты,кухня,кладовка,кбжу,калории,готовка,меню,штрихкод,сканер,семья,еда,срок годности,запасы`

**Description:**

> Sasha’s Kitchen помнит, что лежит в холодильнике и что из этого можно приготовить прямо сейчас. Без рекламы, подписок и регистрации.
>
> Холодильник с одного взгляда
> У каждого продукта видно, сколько осталось и сколько он ещё хранится. Что заканчивается и что скоро испортится — заметно само.
>
> Готовьте из того, что есть
> Рецепты, которые собираются из ваших запасов, — первыми; у остальных видно, чего именно не хватает, и одно нажатие кладёт это в список покупок. Граммы, калории, белки, жиры и углеводы пересчитываются на любое число порций.
>
> Списывается то, что ушло на самом деле
> Нажмите «Приготовил», поправьте граммы, если ушло больше или меньше, — холодильник и журнал готовки обновятся сами.
>
> Список покупок собирается сам
> Всё, что закончилось, попадает в список с разумным количеством. Остальное впишите или отсканируйте штрихкод — продукт найдётся в открытой базе Open Food Facts. Отметка «Купил» пополняет холодильник.
>
> Покупайте вместе
> Пригласите близкого человека в свой список через iCloud. Он увидит его в своём приложении, отметит купленное — и ваш холодильник пополнится — и допишет то, что вы забыли. Холодильник и рецепты остаются только вашими.
>
> iPhone, iPad, Mac и Apple Watch
> Всё синхронизируется через ваш iCloud. На Apple Watch список всегда на запястье: нажмите на позицию, когда она легла в корзину. Список можно отправить в Напоминания настоящим чек-листом или поделиться текстом.
>
> 40 рецептов и 49 продуктов для начала, 11 языков, светлая и тёмная тема. Ваши данные остаются на ваших устройствах и в вашем iCloud.

## Deutsch — German

**Subtitle:** Kühlschrank, Rezepte, Einkauf

**Promotional text:** Teile die Einkaufsliste über iCloud mit deiner Familie: Was sie im Laden abhakt, füllt deinen Kühlschrank auf. Mit Barcode-Scanner und App für die Apple Watch.

**Keywords:** `vorrat,lebensmittel,einkaufsliste,inventar,essen,planer,nährwerte,kalorien,kochen,barcode,familie`

**Description:**

> Sasha’s Kitchen weiß, was in deinem Kühlschrank liegt und was du daraus gleich kochen kannst. Ohne Werbung, ohne Abo, ohne Konto.
>
> Dein Kühlschrank auf einen Blick
> Bei jedem Produkt siehst du, wie viel noch da ist und wie lange es hält. Was knapp wird und was bald abläuft, fällt von selbst auf.
>
> Koche mit dem, was da ist
> Rezepte, die du mit deinem Vorrat kochen kannst, stehen oben; bei den anderen siehst du genau, was fehlt – ein Tippen setzt es auf die Einkaufsliste. Gramm, Kalorien, Eiweiß, Fett und Kohlenhydrate werden für jede Portionenzahl neu berechnet.
>
> Abgebucht wird, was wirklich verbraucht wurde
> Tippe auf „Gekocht“, korrigiere die Gramm, falls es mehr oder weniger war – Kühlschrank und Kochprotokoll aktualisieren sich selbst.
>
> Eine Einkaufsliste, die sich selbst füllt
> Was ausgeht, landet mit einer sinnvollen Menge auf der Liste. Alles andere tippst du ein oder scannst den Barcode – das Produkt wird in der offenen Datenbank Open Food Facts gefunden. Abhaken füllt den Kühlschrank auf.
>
> Gemeinsam einkaufen
> Lade jemanden über iCloud zu deiner Liste ein. Die Person sieht sie in ihrer eigenen App, hakt ab, was sie gekauft hat – und dein Kühlschrank wird aufgefüllt – und ergänzt, was du vergessen hast. Kühlschrank und Rezepte bleiben privat.
>
> Auf iPhone, iPad, Mac und Apple Watch
> Alles wird über deine iCloud synchronisiert. Auf der Apple Watch hast du die Liste am Handgelenk: Tippe auf einen Eintrag, sobald er im Korb liegt. Schicke die Liste als echte Checkliste an Erinnerungen oder teile sie als Text.
>
> 40 Rezepte und 49 Produkte zum Start, 11 Sprachen, helles und dunkles Design. Deine Daten bleiben auf deinen Geräten und in deiner eigenen iCloud.

## Français — French

**Subtitle:** Frigo, recettes et courses

**Promotional text:** Partagez la liste de courses en famille via iCloud : ce qu’ils cochent au magasin remplit votre frigo. Avec un scanner de codes-barres et une app Apple Watch.

**Keywords:** `garde-manger,provisions,inventaire,repas,menu,nutrition,calories,cuisine,code-barres,famille`

**Description:**

> Sasha’s Kitchen sait ce qu’il y a dans votre frigo et ce que vous pouvez en cuisiner tout de suite. Sans publicité, sans abonnement, sans compte à créer.
>
> Votre frigo d’un coup d’œil
> Chaque produit montre ce qu’il en reste et combien de temps il se conserve. Ce qui manque et ce qui périme bientôt saute aux yeux.
>
> Cuisinez avec ce que vous avez
> Les recettes réalisables avec vos provisions passent en premier ; pour les autres, vous voyez exactement ce qui manque, et un geste l’ajoute à la liste de courses. Grammes, calories, protéines, lipides et glucides sont recalculés pour n’importe quel nombre de portions.
>
> On déduit ce qui a vraiment servi
> Touchez « Cuisiné », ajustez les grammes si vous en avez utilisé plus ou moins : le frigo et le journal de cuisine se mettent à jour tout seuls.
>
> Une liste de courses qui se remplit seule
> Ce qui est épuisé arrive sur la liste avec une quantité raisonnable. Ajoutez le reste au clavier ou en scannant un code-barres : le produit est cherché dans la base ouverte Open Food Facts. Cocher un article remplit le frigo.
>
> Faites les courses à plusieurs
> Invitez un proche à votre liste via iCloud. Il la voit dans sa propre app, coche ce qu’il a acheté — et votre frigo se remplit — et ajoute ce que vous avez oublié. Votre frigo et vos recettes restent privés.
>
> Sur iPhone, iPad, Mac et Apple Watch
> Tout se synchronise via votre iCloud. Sur l’Apple Watch, la liste est à votre poignet : touchez un article dès qu’il est dans le panier. Envoyez la liste dans Rappels comme une vraie liste à cocher ou partagez-la en texte.
>
> 40 recettes et 49 produits pour commencer, 11 langues, thèmes clair et sombre. Vos données restent sur vos appareils et dans votre propre iCloud.

## Español — Spanish (Spain)

**Subtitle:** Nevera, recetas y compra

**Promotional text:** Comparte la lista de la compra con tu familia por iCloud: lo que marquen en la tienda llena tu nevera. Con escáner de códigos de barras y app para Apple Watch.

**Keywords:** `despensa,alimentos,inventario,comida,menú,nutrición,calorías,cocinar,código,familia,caducidad`

**Description:**

> Sasha’s Kitchen sabe qué hay en tu nevera y qué puedes cocinar con ello ahora mismo. Sin anuncios, sin suscripciones, sin crear una cuenta.
>
> Tu nevera de un vistazo
> Cada producto muestra cuánto queda y cuánto tiempo aguanta. Lo que se acaba y lo que caduca pronto salta a la vista.
>
> Cocina con lo que tienes
> Primero, las recetas que salen con lo que tienes; en las demás ves exactamente qué falta, y un toque lo añade a la lista de la compra. Gramos, calorías, proteínas, grasas y carbohidratos se recalculan para cualquier número de raciones.
>
> Se descuenta lo que de verdad usaste
> Toca «Cocinado», ajusta los gramos si usaste más o menos, y la nevera y el diario de cocina se actualizan solos.
>
> Una lista de la compra que se llena sola
> Lo que se termina pasa a la lista con una cantidad razonable. Añade lo demás escribiendo o escaneando un código de barras: el producto se busca en la base abierta Open Food Facts. Al marcarlo, la nevera se repone.
>
> Comprad juntos
> Invita a alguien a tu lista por iCloud. La verá en su propia app, marcará lo que compre —y tu nevera se repondrá— y añadirá lo que se te olvidó. Tu nevera y tus recetas siguen siendo privadas.
>
> En iPhone, iPad, Mac y Apple Watch
> Todo se sincroniza con tu iCloud. En el Apple Watch llevas la lista en la muñeca: toca un producto en cuanto esté en la cesta. Envía la lista a Recordatorios como una lista de verdad o compártela como texto.
>
> 40 recetas y 49 productos para empezar, 11 idiomas, temas claro y oscuro. Tus datos se quedan en tus dispositivos y en tu propio iCloud.

## Italiano — Italian

**Subtitle:** Frigo, ricette e spesa

**Promotional text:** Condividi la lista della spesa con la famiglia tramite iCloud: ciò che spuntano al negozio riempie il tuo frigo. Con scanner di codici a barre e app per Apple Watch.

**Keywords:** `dispensa,alimenti,inventario,pasti,menu,nutrizione,calorie,cucinare,codice,barre,famiglia,scadenza`

**Description:**

> Sasha’s Kitchen sa cosa c’è nel tuo frigo e cosa puoi cucinarci subito. Niente pubblicità, niente abbonamenti, nessun account da creare.
>
> Il frigo a colpo d’occhio
> Ogni prodotto mostra quanto ne resta e quanto si conserva. Ciò che sta finendo e ciò che scade presto si nota da solo.
>
> Cucina con quello che hai
> Prima le ricette che si fanno con le tue scorte; per le altre vedi esattamente cosa manca, e un tocco lo mette nella lista della spesa. Grammi, calorie, proteine, grassi e carboidrati si ricalcolano per qualsiasi numero di porzioni.
>
> Si scala quello che hai usato davvero
> Tocca «Cucinato», correggi i grammi se ne hai usati di più o di meno, e frigo e diario di cucina si aggiornano da soli.
>
> Una lista della spesa che si riempie da sola
> Ciò che finisce arriva in lista con una quantità sensata. Aggiungi il resto scrivendolo o scansionando un codice a barre: il prodotto viene cercato nel database aperto Open Food Facts. Spuntarlo riempie il frigo.
>
> Fate la spesa insieme
> Invita qualcuno nella tua lista tramite iCloud. La vedrà nella sua app, spunterà ciò che ha comprato — e il tuo frigo si riempirà — e aggiungerà ciò che hai dimenticato. Frigo e ricette restano privati.
>
> Su iPhone, iPad, Mac e Apple Watch
> Tutto si sincronizza con il tuo iCloud. Su Apple Watch hai la lista al polso: tocca un articolo appena è nel carrello. Invia la lista a Promemoria come una vera checklist o condividila come testo.
>
> 40 ricette e 49 prodotti per iniziare, 11 lingue, tema chiaro e scuro. I tuoi dati restano sui tuoi dispositivi e nel tuo iCloud.

## Português (Brasil) — Portuguese (Brazil)

**Subtitle:** Geladeira, receitas e compras

**Promotional text:** Compartilhe a lista de compras com a família pelo iCloud: o que marcarem no mercado abastece sua geladeira. Com leitor de código de barras e app para Apple Watch.

**Keywords:** `despensa,alimentos,estoque,refeição,cardápio,nutrição,calorias,cozinhar,código,família,validade`

**Description:**

> O Sasha’s Kitchen sabe o que tem na sua geladeira e o que dá para cozinhar com isso agora mesmo. Sem anúncios, sem assinatura, sem criar conta.
>
> Sua geladeira num relance
> Cada produto mostra quanto resta e por quanto tempo dura. O que está acabando e o que vence logo aparece em destaque.
>
> Cozinhe com o que tem
> Primeiro vêm as receitas que dá para fazer com o seu estoque; nas outras você vê exatamente o que falta, e um toque coloca isso na lista de compras. Gramas, calorias, proteínas, gorduras e carboidratos são recalculados para qualquer número de porções.
>
> Desconta o que foi usado de verdade
> Toque em “Cozinhei”, ajuste os gramas se usou mais ou menos, e a geladeira e o diário de cozinha se atualizam sozinhos.
>
> Uma lista de compras que se monta sozinha
> O que acaba entra na lista com uma quantidade sensata. Adicione o resto digitando ou lendo um código de barras: o produto é buscado na base aberta Open Food Facts. Marcar um item abastece a geladeira.
>
> Façam as compras juntos
> Convide alguém para a sua lista pelo iCloud. A pessoa vê a lista no próprio app, marca o que comprou — e sua geladeira é abastecida — e acrescenta o que você esqueceu. Geladeira e receitas continuam só suas.
>
> No iPhone, iPad, Mac e Apple Watch
> Tudo sincroniza pelo seu iCloud. No Apple Watch a lista fica no pulso: toque num item assim que ele entrar no carrinho. Envie a lista para Lembretes como uma checklist de verdade ou compartilhe como texto.
>
> 40 receitas e 49 produtos para começar, 11 idiomas, temas claro e escuro. Seus dados ficam nos seus aparelhos e no seu próprio iCloud.

## 日本語 — Japanese

**Subtitle:** 冷蔵庫・レシピ・買い物リスト

**Promotional text:** 買い物リストをiCloudで家族と共有。お店でチェックされた分が、あなたの冷蔵庫に補充されます。バーコードスキャナとApple Watchアプリ付き。

**Keywords:** `食材,在庫,管理,献立,料理,栄養,カロリー,PFC,バーコード,家族,賞味期限,消費期限,ストック,自炊`

**Description:**

> Sasha’s Kitchenは、冷蔵庫に何があって、今すぐ何が作れるかを覚えておいてくれるアプリです。広告なし、サブスクなし、アカウント登録も不要です。
>
> 冷蔵庫がひと目でわかる
> 食材ごとに残量とあと何日もつかを表示。残りわずかなもの、期限が近いものは自然と目に入ります。
>
> あるもので料理
> 今ある食材で作れるレシピが先頭に。それ以外のレシピは足りないものがはっきりわかり、ワンタップで買い物リストに追加できます。グラム、カロリー、たんぱく質、脂質、炭水化物は何人分でも自動で再計算されます。
>
> 実際に使った分だけ差し引き
> 「作った」をタップし、多めや少なめに使ったときはグラムを調整。冷蔵庫と料理の記録が自動で更新されます。
>
> ひとりでにできあがる買い物リスト
> 使い切ったものは、ちょうどいい量でリストに入ります。ほかのものは入力するか、バーコードをスキャン。オープンなデータベースOpen Food Factsで商品を探します。チェックすると冷蔵庫に補充されます。
>
> 買い物はいっしょに
> iCloudで家族をリストに招待できます。相手は自分のアプリでリストを見て、買ったものにチェック(あなたの冷蔵庫に補充されます)、忘れていたものも追加できます。冷蔵庫とレシピは共有されません。
>
> iPhone、iPad、Mac、Apple Watchで
> すべてあなたのiCloudで同期。Apple Watchなら手首でリストを確認し、かごに入れたらタップするだけ。リストはリマインダーにチェックリストとして送ったり、テキストで共有したりできます。
>
> 最初から40のレシピと49の食材、11言語、ライト・ダークテーマ対応。データはあなたのデバイスとあなた自身のiCloudにだけ保存されます。

## 한국어 — Korean

**Subtitle:** 냉장고, 레시피, 장보기 목록

**Promotional text:** iCloud로 가족과 장보기 목록을 공유하세요. 마트에서 체크한 만큼 내 냉장고가 채워집니다. 바코드 스캐너와 Apple Watch 앱 포함.

**Keywords:** `식재료,재고,관리,식단,요리,영양,칼로리,탄단지,바코드,가족,유통기한,식품,자취,냉파`

**Description:**

> Sasha’s Kitchen은 냉장고에 무엇이 있는지, 지금 바로 무엇을 만들 수 있는지 기억해 줍니다. 광고도, 구독도, 계정 가입도 없습니다.
>
> 냉장고를 한눈에
> 재료마다 남은 양과 보관 기간을 보여 줍니다. 떨어져 가는 것과 곧 상하는 것이 저절로 눈에 띕니다.
>
> 있는 재료로 요리
> 가진 재료로 만들 수 있는 레시피가 먼저 나오고, 나머지는 무엇이 부족한지 정확히 보여 줍니다. 탭 한 번이면 장보기 목록에 추가됩니다. 그램, 칼로리, 단백질, 지방, 탄수화물은 몇 인분이든 다시 계산됩니다.
>
> 실제로 쓴 만큼만 차감
> ‘만들었음’을 탭하고 더 쓰거나 덜 썼다면 그램을 고치세요. 냉장고와 요리 기록이 알아서 업데이트됩니다.
>
> 저절로 채워지는 장보기 목록
> 다 떨어진 재료는 적당한 양으로 목록에 올라갑니다. 다른 것은 직접 입력하거나 바코드를 스캔하세요. 열린 데이터베이스 Open Food Facts에서 제품을 찾아 줍니다. 체크하면 냉장고가 채워집니다.
>
> 장보기는 함께
> iCloud로 가족을 내 목록에 초대하세요. 상대는 자기 앱에서 목록을 보고, 산 것을 체크하고(내 냉장고가 채워집니다), 내가 잊은 것을 추가할 수 있습니다. 냉장고와 레시피는 공유되지 않습니다.
>
> iPhone, iPad, Mac, Apple Watch에서
> 모든 것이 내 iCloud로 동기화됩니다. Apple Watch에서는 손목에서 목록을 보고, 장바구니에 담을 때마다 탭하면 됩니다. 목록을 미리 알림에 체크리스트로 보내거나 텍스트로 공유할 수도 있습니다.
>
> 시작용 레시피 40개와 재료 49개, 11개 언어, 라이트·다크 테마. 데이터는 내 기기와 내 iCloud에만 저장됩니다.

## 简体中文 — Chinese (Simplified)

**Subtitle:** 冰箱、菜谱和购物清单

**Promotional text:** 通过 iCloud 与家人共享购物清单：对方在超市勾选的东西，会自动补进你的冰箱。附带条形码扫描和 Apple Watch 应用。

**Keywords:** `食材,库存,管理,做饭,菜单,营养,热量,卡路里,条形码,家庭,保质期,买菜,厨房助手`

**Description:**

> Sasha’s Kitchen 帮你记住冰箱里有什么，以及现在能用它们做什么。没有广告，没有订阅，无需注册账户。
>
> 冰箱一目了然
> 每样食材都显示还剩多少、还能放几天。快用完的和快过期的，一眼就能看到。
>
> 用现有食材做饭
> 能用现有食材做的菜谱排在最前面；其他菜谱会清楚列出缺什么，轻点一下就加入购物清单。克数、热量、蛋白质、脂肪和碳水会按任意份数重新计算。
>
> 只扣除实际用掉的
> 轻点“做好了”，用多用少都可以调整克数，冰箱和烹饪记录会自动更新。
>
> 自动生成的购物清单
> 用完的东西会以合适的数量进入清单。其他东西可以手动输入或扫描条形码，应用会在开放数据库 Open Food Facts 中查找商品。勾选后冰箱会自动补货。
>
> 一起去采购
> 通过 iCloud 邀请家人加入你的清单。对方在自己的应用里就能看到，勾选买到的东西（你的冰箱随之补货），还能补上你忘记的。你的冰箱和菜谱不会被共享。
>
> iPhone、iPad、Mac 和 Apple Watch
> 一切通过你的 iCloud 同步。在 Apple Watch 上，清单就在手腕上：放进购物篮时轻点一下即可。还可以把清单作为真正的待办清单发送到“提醒事项”，或以文本分享。
>
> 自带 40 道菜谱和 49 种食材，支持 11 种语言及浅色和深色主题。你的数据只保存在你的设备和你自己的 iCloud 中。

## ไทย — Thai

**Subtitle:** ตู้เย็น สูตรอาหาร รายการซื้อ

**Promotional text:** แชร์รายการซื้อของกับครอบครัวผ่าน iCloud: ของที่อีกฝ่ายติ๊กในร้านจะเติมเข้าตู้เย็นของคุณ มาพร้อมเครื่องสแกนบาร์โค้ดและแอปบน Apple Watch

**Keywords:** `วัตถุดิบ,สต็อก,จัดการ,เมนู,ทำอาหาร,โภชนาการ,แคลอรี่,บาร์โค้ด,ครอบครัว,วันหมดอายุ,ของกิน,ซื้อของ`

**Description:**

> Sasha’s Kitchen จำได้ว่าในตู้เย็นมีอะไรบ้าง และตอนนี้ทำอะไรจากของเหล่านั้นได้ ไม่มีโฆษณา ไม่มีการสมัครสมาชิก ไม่ต้องสร้างบัญชี
>
> ดูตู้เย็นได้ในพริบตา
> วัตถุดิบแต่ละอย่างบอกว่าเหลือเท่าไรและเก็บได้อีกกี่วัน ของที่ใกล้หมดและใกล้หมดอายุจะเห็นได้เอง
>
> ทำอาหารจากของที่มี
> สูตรที่ทำได้จากของที่มีจะขึ้นก่อน ส่วนสูตรอื่นจะบอกชัดว่าขาดอะไร แตะครั้งเดียวก็ใส่ลงรายการซื้อของ กรัม แคลอรี โปรตีน ไขมัน และคาร์บ คำนวณใหม่ได้ทุกจำนวนที่
>
> หักเฉพาะที่ใช้จริง
> แตะ “ทำแล้ว” ปรับกรัมได้ถ้าใช้มากหรือน้อยกว่าสูตร ตู้เย็นและบันทึกการทำอาหารจะอัปเดตเอง
>
> รายการซื้อของที่เติมตัวเอง
> ของที่หมดจะเข้ารายการพร้อมปริมาณที่เหมาะสม ของอื่นพิมพ์เองหรือสแกนบาร์โค้ด แอปจะค้นหาสินค้าในฐานข้อมูลเปิด Open Food Facts ติ๊กแล้วตู้เย็นจะถูกเติม
>
> ช่วยกันซื้อของ
> เชิญคนในบ้านเข้ารายการของคุณผ่าน iCloud เขาจะเห็นรายการในแอปของตัวเอง ติ๊กของที่ซื้อแล้ว (ตู้เย็นของคุณจะถูกเติม) และเพิ่มของที่คุณลืมได้ ตู้เย็นและสูตรอาหารยังเป็นของคุณคนเดียว
>
> บน iPhone, iPad, Mac และ Apple Watch
> ทุกอย่างซิงค์ผ่าน iCloud ของคุณ บน Apple Watch รายการอยู่บนข้อมือ แตะรายการเมื่อหยิบใส่ตะกร้า ส่งรายการไปยังแอปเตือนความจำเป็นเช็กลิสต์จริง หรือแชร์เป็นข้อความก็ได้
>
> มีสูตรอาหาร 40 สูตรและวัตถุดิบ 49 อย่างให้เริ่มต้น รองรับ 11 ภาษา ธีมสว่างและมืด ข้อมูลของคุณอยู่บนอุปกรณ์และใน iCloud ของคุณเท่านั้น
