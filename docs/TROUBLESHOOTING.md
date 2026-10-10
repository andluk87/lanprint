# Диагностика

| Симптом | Проверка / действие |
|---|---|
| Сборка не видит Docker | `docker version`, запущенный daemon, права на socket. Не отключайте TLS/проверку образа. |
| Docker не может писать свой config | Для среды с read-only профилем задайте отдельный writable `DOCKER_CONFIG=/tmp/lanprint-docker` перед `bash build.sh`. В обычном VPS это не требуется. Не копируйте чужие credentials. |
| Пустой список принтеров | `Get-Printer`, состояние Spooler, установленный драйвер, права текущего пользователя. |
| IP отсутствует у интерфейса | «Обновить», проверьте `Get-NetIPAddress`; listener запрещает bind к чужому IP/0.0.0.0. |
| Ошибка bind | `Get-NetTCPConnection -LocalPort 9100,8631,8765`; остановите свой конфликтующий сервис либо поменяйте порты в config и Firewall. |
| Нет DHCP / адрес 169.254.* | DHCP сервер/кабель/Hyper-V switch/несколько MAC на порту; проверьте роутер. |
| Static Duplicate/Tentative | `Get-NetIPAddress`; выберите свободный адрес вне DHCP pool или используйте резервацию. |
| Bridge недоступен | Windows Home не поддерживает Hyper-V. Проверьте виртуализацию, Ethernet, switch и выделенный LANPrint-Printer NIC. Shared — отдельный режим с IP ПК. |
| Windows изменил основной IP после switch | Hyper-V переносит сетевой доступ на host vEthernet. Восстановите исходные параметры из локальной консоли; не удаляйте switch удалённо через него. |
| Клиент не подключается | Конкретный IP/порт, Windows Firewall Private profile, CIDR ACL приложения, client isolation на Wi-Fi, VLAN/routing. Не публикуйте порты на VPS для runtime. |
| После DHCP служба работает, но клиент не видит | Адрес-специфичный Firewall rule нужно обновить. Обновите порт клиента/используйте DHCP reservation. GUI автоматически меняет bind, но не Firewall. |
| Нет обнаружения | UDP5353, multicast на роутере, выбранная NIC, сторонний mDNS daemon. Посмотрите журнал `mDNS unavailable`; ручной IPP URI работает независимо от discovery. Автообнаружение требует отдельной аппаратной проверки. |
| IPP клиент отказывает | Это базовый IPP, не IPP Everywhere. Отправляйте text/plain, image/png, image/jpeg; PDF/PWG/URF/Office не поддерживаются. |
| RAW печатает мусор | Несовместимый PDL или RAW passthrough выключен. Не отправляйте произвольный PDF/Office/EMF как RAW. |
| «spooled», но листа нет | Windows queue, offline/error состояние драйвера, бумага, выбранный порт/USB. Spooler acceptance не равен физическому completion. |
| Не удаётся отменить | LANPrint ID и Windows Spooler ID различаются; используйте ID из LANPrint log/API для кнопки. Уже напечатанный/удалённый Spooler job может быть неотменяемым. |
| Текст слишком длинный/вид шрифтов | UTF-8 без NUL, встроенное разбиение строк/страниц. Для сложной вёрстки подготовьте PNG/JPEG; универсального PDF layout нет. |
| HTTP 401 | Правильный токен, `Authorization: Bearer …`; не используйте токен в URL. |
| После reboot не запустилось | Автозапуск требует входа того же пользователя, HKCU Run и неизменённого EXE пути. Это не системная служба. |

Журналы `%LOCALAPPDATA%\LANPrint\lanprint.log` и `.log.1`, по ~2 MiB каждый. Последние 256 метаданных заданий хранятся в памяти; данные освобождаются после передачи/ошибки/отмены. Файлы документов на диск не сохраняются. При смене bind/остановке незавершённые задания LANPrint отменяются; отправленные задания остаются в Windows.

Для отката автозапуска снимите флажок и сохраните. Для удаления Firewall правил используйте `Get-NetFirewallRule -DisplayName 'LANPrint TCP','LANPrint mDNS' | Remove-NetFirewallRule` в elevated PowerShell. Удаление Hyper-V switch требует проверки зависимостей и восстановления основной сети; выполнять только с локальной консоли.
