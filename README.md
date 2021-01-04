# bankobot
## Description
- This is a helper for sublime bot in telegram (specifically for the /pidor game). It reminds you every day in case you missed to call it. From chat perspective, it's enought to just add it as a member, and it does all the work
- Twice a day at 4:20 a random message
- /settz command allows setting your timezone to make sure you get the message at the right time

## Design
### Persistent info
Chats are memorized in the local sqlite file. It keeps info such as chat ID, timezone and the last time a notification message was sent 
(in case of restarts)
### Messages
All messages are kept in text files, one message per line
### App architecture
Main worker is running a job and has central access to all non-pure operations: changing timezone, sending message to a chat, registering a chat internally etc.
All other workers send jobs to this channel.

In the beginning of a day a job registers every chat to remind to call /pidor in the evening (by Kiev time). Issuing a /pidor command makes bot remember you until the end of the day to omit sending unnecessary reminder. Also once a day every chat gets notified at the same time.

For the random messages, jobs are scheduled depending on their timezones. Mostly, same approach applies.

Other jobs are reactionary to what messages are sent to chat.