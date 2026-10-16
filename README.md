# Virfield

Virfield даёт AI-агенту временные macOS-машины для сборок, тестов и отладки.
Ты описываешь задачу в Codex или Claude Code, агент получает машину, работает
на ней по SSH и сохраняет результаты. Virfield управляет образами, доступом,
сроком работы машин и их удалением.

## Что можно делать

- Собирать и тестировать проект на нужной macOS с подходящей версией Xcode.
- Запускать UI-тесты на подготовленных машинах и открывать их рабочий стол.
- Создавать образы из разрешённых macOS/Xcode-профилей и переиспользовать их.
- Импортировать и публиковать Tart-образы в настроенном реестре GitHub (GHCR), переносить их между хранилищами.
- Получать доступ к подключённым физическим macOS/Linux-узлам.
- Смотреть свободные ресурсы, продлевать время работы машины и разбирать ошибки запуска.

Команды сборки и тестов агент выполняет через свой терминал по SSH.
Набор доступных систем зависит от настроенных образов и оборудования.

## Установка

Для локальных macOS VM нужен Mac с Apple Silicon и macOS 13 или новее. Установи Go версии из
[go.mod](go.mod), Python 3, инструменты разработчика Xcode и
[Tart](https://tart.run/quick-start/). Для создания образов нужен также
[Packer](https://developer.hashicorp.com/packer/install).

### 1. Скачать и собрать

```sh
git clone https://github.com/mallexxx/virfield.git
cd virfield
make check
make build
```

### 2. Создать установку

Если Virfield уже установлен и запущен, переходи к подключению MCP, используя
путь к существующей установке. Для новой установки:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
./bin/virfield init "$VIRFIELD_HOME" macos-golden macos-golden local
mkdir "$VIRFIELD_HOME/bin"
cp bin/virfield bin/virfieldd bin/virfield-mcp "$VIRFIELD_HOME/bin/"
cp -R bin/VirfieldAppleBrowser.app "$VIRFIELD_HOME/bin/"
```

В `$VIRFIELD_HOME/config.json` настрой хранилище, разрешённые базовые образы
и пути к Packer и кешу Xcode по [инструкции настройки](docs/OPERATIONS.md#configure-storage-and-credentials).
`init` создаёт конфигурацию и токен; образ macOS он не скачивает.
Для внешнего диска может понадобиться Full Disk Access. Подпись через
`make build SIGN_IDENTITY="YOUR_SIGNING_IDENTITY"` сохраняет это разрешение между сборками.

### 3. Запустить

```sh
"$VIRFIELD_HOME/bin/virfield" config-check "$VIRFIELD_HOME/config.json"
"$VIRFIELD_HOME/bin/virfieldd" -config "$VIRFIELD_HOME/config.json"
```

Оставь этот терминал открытым. Для автозапуска используй
[настройку службы](docs/OPERATIONS.md#install).

## Подключить MCP

На том же Mac открой второй терминал и укажи путь к установке и адрес Virfield:

```sh
VIRFIELD_HOME="$HOME/Library/Application Support/Virfield"
VIRFIELD_URL="http://127.0.0.1:7780"
```

Выбери свой клиент.

**Codex:**

```sh
codex mcp add virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
codex mcp get virfield
```

**Claude Code:**

```sh
claude mcp add --transport stdio --scope user virfield -- "$VIRFIELD_HOME/bin/virfield-mcp" -url "$VIRFIELD_URL" -token-file "$VIRFIELD_HOME/token"
claude mcp get virfield
```

Для другого MCP-клиента есть [пример конфигурации stdio](deploy/mcp.json.example).
Передавай путь к файлу токена, а не его содержимое. Затем переподключи MCP
в своём клиенте и открой новый чат.

## Что говорить агенту

Проверить подключение:

> Прочитай инструкции Virfield через virfield_help, затем проверь virfield_status.
> Покажи доступные машины и образы. Если настройка не закончена, объясни, чего не хватает.

Собрать и проверить проект:

> Через Virfield возьми macOS 12.7.6 с Xcode 13.4.1 на два часа.
> Скопируй туда этот проект, запусти его тесты и сохрани отчёт в моём проекте.
> После сохранения результатов удали только созданную для этой задачи машину.

Подготовить образ:

> Через Virfield создай новый образ macOS с нужной этому проекту версией Xcode.
> Сначала проверь доступные версии и место на диске; если подходящего базового образа нет, сообщи.

Посмотреть, что происходит в VM:

> Открой рабочий стол машины Virfield, на которой сейчас идут мои тесты.

В задаче указывай нужные версии, проект, время работы и какие результаты сохранить.
Удаление временной машины можно разрешить сразу, как в примере выше.

## Подробные инструкции

- [Установка, узлы, резервные копии и восстановление](docs/OPERATIONS.md)
- [Создание и перенос образов](docs/IMAGE-PIPELINE.md)
- [Инструменты MCP и работа агента](docs/AGENT-GUIDE.md)
- [Требования к окружению](docs/ENVIRONMENT.md)
- [Отладка через VS Code](docs/REMOTE-DEBUG.md)
- [Разработка и сборка релиза](docs/ARCHITECTURE.md)
