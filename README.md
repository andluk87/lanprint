# LANPrint 0.1.1

Приложение для Windows 10 x64: принимает сетевые задания и печатает через установленный драйвер локального принтера. **Docker используется только для сборки на Linux VPS**. На Windows запускается самостоятельный `LANPrint.exe`, Docker, Go и .NET не нужны.

## Сборка на VPS

Нужны Git и Docker Engine с BuildKit; проверенная версия Docker — 28.4.0. Go 1.24.7 и базовый образ зафиксированы в Dockerfile по версии и SHA-256 digest. Сторонних Go-библиотек нет.

```bash
git clone https://github.com/andluk87/lanprint.git
cd lanprint
bash build.sh
```

В корне появятся `LANPrint.exe` и `LANPrint.exe.sha256`. Тесты также выполняются внутри контейнера. Альтернатива: `docker compose run --build --rm build`.

Скачивание с VPS в PowerShell Windows:

```powershell
scp user@YOUR_VPS:/path/to/lanprint/LANPrint.exe .
scp user@YOUR_VPS:/path/to/lanprint/LANPrint.exe.sha256 .
Get-FileHash .\LANPrint.exe -Algorithm SHA256
.\LANPrint.exe
```

Сравните SHA-256 с файлом контрольной суммы. Для сетевых вспомогательных скриптов и документации скачайте/клонируйте весь репозиторий. Неподписанный EXE может вызвать предупреждение SmartScreen.

## Что реализовано

- Нативное окно Windows: интерфейсы, MAC/IPv4/маска/шлюз, DHCP/Static, принтеры и состояние, запуск/остановка, тестовая печать, журнал, трей, автозапуск при входе пользователя.
- TCP 9100: по умолчанию UTF-8 текст с рендерингом через GDI/драйвер Windows; явно включаемый RAW passthrough для совместимого PCL/PostScript/иного PDL.
- Базовый IPP 1.1/2.0: Print-Job, Validate-Job, Get-Printer-Attributes, Get-Jobs, Get-Job-Attributes, Cancel-Job. Отмена требует Bearer-токен.
- IPP форматы: `text/plain`, `image/png`, `image/jpeg`; `application/octet-stream` только при включённом RAW passthrough.
- mDNS/DNS-SD: `_ipp._tcp` и `_pdl-datastream._tcp` на выбранном адаптере.
- Очередь с последовательной передачей в Windows Print Spooler, отмена, ограниченные память и журнал, CIDR ACL, HTTP управление с токеном.
- GUI следит за адресом выбранного адаптера и перепривязывает службы после изменения DHCP/восстановления сети. Очередь в памяти не сохраняется при остановке/смене адреса.
- Shared IP или отдельный адаптер Hyper-V External Switch; создание последнего требует явного подтверждения и UAC.
- GitHub Actions: Docker-сборка и тесты, артефакты EXE/SHA-256; тег соответствующей версии создаёт GitHub Release.

## Границы версии и проверки

Это первая реализация, **не прошедшая полную аппаратную приёмку ТЗ**. Linux-тесты проверяют настоящие TCP/HTTP/IPP запросы с заменой Windows-бэкенда. Windows GUI, USB-принтер, Windows Print Spooler, Hyper-V DHCP/Static/Bridge и обнаружение реальными клиентами требуют проверки на Windows 10 и в вашей LAN. Эти результаты нельзя вывести из успешной кросс-сборки.

PDF, Office, PWG Raster, URF, IPPS/TLS, AirPrint/Mopria и полный IPP Everywhere не реализованы и не рекламируются. IPP-клиент должен отправлять поддерживаемый формат; автоматическая установка универсального драйвера не гарантируется. Для обычной печати из Windows через TCP 9100 нужен драйвер, выдающий поддерживаемый физическим принтером PDL, и явно включённый RAW passthrough. Host-based/GDI-only принтеры в этом режиме могут быть несовместимы; для них используйте поддерживаемые IPP форматы с рендерингом на сервере.

`spooled` означает успешную передачу в Spooler, а не подтверждение отпечатанного листа. Состояние физического устройства зависит от драйвера. «Очередь Windows» показывает текущие задания Spooler. Приложение работает в пользовательской сессии, не как Windows Service; после перезагрузки автозапуск выполняется при входе пользователя.

## Документация

- [Установка, DHCP/Static, Bridge и клиентская печать](docs/INSTALLATION.md)
- [Сборка на VPS и выпуск](docs/BUILD.md)
- [Диагностика](docs/TROUBLESHOOTING.md)
- [Протоколы, ограничения и безопасность](docs/ARCHITECTURE.md)
- [Матрица проверок](docs/TESTING.md)
- [Исходное ТЗ](docs/TECHNICAL_SPECIFICATION.md)

Настройки и журналы: `%LOCALAPPDATA%\LANPrint\`. Пример: [config/config.example.json](config/config.example.json). Не публикуйте действующий `admin_token`.

## Исправление 0.1.1

Разделены stdout JSON и stderr PowerShell CLIXML/progress, добавлена регрессионная проверка. Поиск принтеров выполняется даже при ошибке списка адаптеров; адаптеры без IPv4 также отображаются для настройки DHCP/Static.
