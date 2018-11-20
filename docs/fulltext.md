# Full-Text Search in Cozy Cloud
---

Using the [Bleve](http://blevesearch.com/) library, this git repository intends to contribute to the [cozy-stack](https://github.com/cozy/cozy-stack) by implementing full-text search functionalities.


## Goals

The goals are to efficiently suggest an architecture for full-text search, to prototype a solution and to test it, while taking into account the cozy-stack constraints :

* separation and autonomy between the indexation and the query sides,
* storage of the index on disk,
* one index per user and per [doctype](https://docs.cozy.io/en/cozy-doctypes/docs/README/) (and optionally per language),
* scalability constraints (in term of disk space, indexation time, and query time),
* update of the index with a notification queue.

## Context

This implementation takes place in the context of the Qwant - CozyCloud partnership. It started in mid June 2018.

## Scope

In this module, we do not extract text from file, which means that another module must be implemented to return the file content. Except for files, all the information to index are contained in a [CouchDB](http://couchdb.apache.org/). 

To quickly get started, we focused on three doctypes: photo albums, files and bank accounts. We also considered two languages of indexation: French and English.

## Indexation

The indexation code is at [pkg/index/index.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/fulltext/indexation/index.go).

The indexes are stored on the default KVStore: [BoltDB](https://github.com/boltdb/bolt).

The function to start the indexes on the indexation side is `StartIndex()`. It takes a list of instance and a list of docTypes to index as arguments. It loads the language detection model and initializes global vars.
 
It calls `initializeIndexes()` for each instance. This function allocates the right `InstanceIndex` object and store it in `indexes` and call `initializeIndexDocType()` on each docType. This last function will allocate the mapping between `docType`, `lang` and `bleve.Index`. It will then call `getIndex()` for each.
The `getIndex()` function either fetches the index from the disk or creates it if not found. In the second case, it sets their mapping and stores their couchdb sequence number to 0 (the default). No indexation is made at this stage however.
We now have one index per instance, per doctype (3 in our examples) and per language that are initialized.

Following this initialization, the `StartIndex()` function starts the worker that is going to process the notification queue. It finally calls `UpdateAllIndexes()`, that iterates on all the indexes calling `AddUpdateIndexJob()` on each.

All indexing operations are made with a mutex. There is one mutex per instance. The following functions all lock the mutex to protect the indexes and to make sure the index won't be modified/removed along the way: `IndexUpdate()`, `DeleteIndexLock()`, `DeleteAllIndexesInstance()`, `Replicate()`, `ReplicateAll()`, `ReIndex()`, `initializeIndexes()`.

On the contrary, the following function should be called only inside a mutex lock: `deleteIndex()`, `initializeIndexDocType()`, `getIndex()`, `setStoreSeq()`, `getStoreSeq()`, `setStoreMd5sum()`, `getStoreMd5sum()`, `getExistingContent()`.

### Workers and Jobs

A notification system is used for indexing, instead of a periodic trigger. It allows the indexer to fetch updates in `CouchDB` only in case of a modification (creation, update, deletion).

Notification for update jobs are sent to `AddUpdateIndexJob()`. This function adds the couple `(instanceName, docType)` to a job queue (a golang channel).

The update worker is processing those jobs. It first calls `IndexUpdate()` and if this operation was successful, it calls `sendIndexToQuery()` to replicate the updated index on the query side (over http).

### Indexing

`IndexUpdate()` fetch the last couchdb sequence recorded in the index and use `couchdb.GetChanges()` to retrieve all the changes since that last sequence number. Since it is 0 when an index is created, it fetches all files in this couchdb.

Afterwards, there are three cases to take into account :

* the document is new. It means we have to detect its language and index it in the corresponding index.
* the document is updated and was already indexed. In this case, we need to find which index it belonged to, independently of its current language, to prevent having it indexed in two different indexes, and reindex it. Bleve doesn't allow to update specific fields, the entire document must be reindexed ([Bleve Google Groups](https://groups.google.com/forum/?utm_medium=email&utm_source=footer#!msg/bleve/v1E57t5lU3U/QTMLxaK9BwAJ)) : 

> There is no mechanism to update a single field value for a document.  You have to provide the entire document again, containing the updated field value.

* the document was indexed and is now trashed or destroyed. We have to find the index it belonged to and delete it from this index.

In term of implementation, since we don't know if the document is new or updated, we look for it in the indexes and if we couldn't find it, we consider it new.

The indexation is done by batch, which means that we have a batch for every language and we index them every 300 documents (in total, not per language). *The choice of 300 should be tested on the cozy architecture.*

Finally, the new sequence number returned by `couchdb.GetChanges()` is stored in every index. Thus, 2 indexes of the same doctype (French and English) will both have the same sequence number.

#### File content
In the particular case of files, we need to fetch the content separately and to index it. Using a mock `getContentFile()` function, we implement a solution using the `md5sum` field that let us know the `md5sum` of the file content. 

When indexing a file for the first time, we use `getContentFile()` to add it to the `content` field, that is mapped as a `textField`. Then we store the `md5sum` in the underlying store (similarly to the sequence number). 

When the file is returned on `couchdb.GetChanges()`, we check for the `md5sum` to compare it with the stored value to know if the content has been updated and we need to fetch it or not. If this is the case, we proceed as previously. Else, since an update operation overwrites the document indexed previously, we still need to fetch the previous content. We then call the `getExistingContent()` that returns what had been stored in index.

This solution implies that we store the content, and not just the indexed version. It might be limiting as file content may be very heavy.
The other alternative would be to index the content in a separate index. It means that we should manage both indexes accordingly, when creating, updating and deleting a file, but also when manipulating the index (replicating, reindexing, sending it to the query side).

*The pros and the cons should be weighed to decide on a solution.*

### Mapping

Each index has its appropriate mapping (see [pkg/index/mapping.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/fulltext/indexation/mapping.go)).

The mapping for each docType is described in a json file. The structure of the file is the following one:
`{
    "name": "textField",
    "tags": "keywordField"
}`. The structure can be nested.

We use 6 different types of field mapping: 

* a `textField` mapping, with 2 different analyzers : either the "fr" or the "en" analyzer, depending on the language. Bleve offers a [wizard](http://analysis.blevesearch.com/analysis) to see the behavior of the different analyzers, and to help create your own. For example, it might be relevant to create an analyzer for file names, so that "\_" is used as a token delimiter. A file such as "cozy\_cloud\_index.md" would then be returned on the query "index".
* a `dateField` mapping, that accepts ISO 8601 format.
* a `numberField` mapping, that accepts int, but convert numbers to float64 internally.
* a `storeField` mapping, that tells the indexer to store the text but not index it.
* a `keywordField` that doesn't apply any analysis to the text and index it as such.
* a `timestampField` mapping, that accepts Linux timestamp format.

By default, any other field will be ignored as `documentMapping.Dynamic` is set to `false`.

The correct document mapping is applied depending on the `docType` field in the document, but as indexes contain only one type of document, it could just be set to default mapping per index.

The usage of numeric fields should be selective ([Bleve Issue 831](https://github.com/blevesearch/bleve/issues/831)):

> In bleve today numeric fields are very expensive to index, as they are optimized for later doing numeric range searches. But, this optimization means that numeric fields can take up to 16x the space of text field with a single term. This is something we hope to improve in the future, but for now it means you have to be very selective about including numeric fields. Having lots of numeric fields means the index will be quite large (and consequently slow). 

### Language Detection

We use a [FastText](https://fasttext.cc) model ([lid.176.ftz](https://fasttext.cc/docs/en/language-identification.html)), to detect the language of a document.
Using a fasttext-go [wrapper](https://github.com/maudetes/fasttextgo), we can load the model once and then feed it with text to obtain the language prediction (see [pkg/index/language\_detection.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/fulltext/indexation/language\_detection.go)).

For now, we predict the language based on the file name only, however, other fields might also be relevant for language identification. Moreover, in the case of the document being a file, the content should obviously be used to predict the language. 

Since we limit the languages to French and English, we iterate on the predicted languages to stop once we encounter one of these two. Indeed, if the file is actually Spanish, it will still classify it as the most probable language between French and English.

The language detection doesn't necessarily returns all languages as prediction, and in particular it sometimes happens to return neither "fr" nor "en". By default, we return "en" in this case.

### Reindex

The `ReIndexAll()` function takes an instance name as argument and calls `ReIndex()` on all the indexes from this instance.

The `ReIndex()` function removes the index from the disk (if found) and creates a new one.

In order to reindex, it starts by checking if the instance has any index associated already. If not, it initializes an `InstanceIndex` object. This way, it can lock the mutex. 
Then it checks whether this particular doctype index existed in this instance. If this is the case, it removes it calling `deleteIndex()`. 
It then re-initializes the index for this doctype calling `initializeIndexDocType()`. It also adds the doctype to the doctype list if wasn't in the list already. 
Finally, it adds an update job for this couple `(instance, doctype)` calling `AddUpdateIndexJob()`.

### Replicate

The `ReplicateAll()` function calls `Replicate()` on each index.

`Replicate()` obtains an `io.Writer` from the specified path. Next, it uses the `.Advanced()` function from bleve to access the store and get a reader. Then, it uses the `WriteTo()` function implemented in the Index branch of the repository [QwantResearch/bleve](https://github.com/QwantResearch/bleve/tree/Index) and writes into a tmp file.

This `WriteTo()` function was implemented for BoltDB only. It uses the `tx.WriteTo()` function from the store. From the BoltDB documentation : 
> You can use the Tx.WriteTo() function to write a consistent view of the database to a writer. If you call this from a read-only transaction, it will perform a hot backup and not block your other database reads and writes.

Finally, it closes the file and the reader and returns the file name.

### Set and Get the sequence number

The functions `setStoreSeq()` and `getStoreSeq()` are used to store and retrieve the sequence number associated with an index, so that we can fetch the changes since this last sequence number.

In order to store the sequence number directly in the index, we use the bleve `SetInternal()` function that allows us to store additional information into the store, without it being taken into account in the index.

The `GetInteral()` function allows us to retrieve this information.

### Delete

The `DeleteAllIndexesInstance()` function calls `deleteIndex()` on each docType from the instance name passed as argument. It then remove the instance from the `indexes` var and remove all indexes from the disk calling `os.RemoveAll()`. 

The `DeleteIndexLock()` function locks a mutex and calls `deleteIndex()`.

The `deleteIndex()` function removes the index for a particular instance and doctype. It closes the indexes for all langs and then removes the indexes from the disk. If `querySide` is set to true, it calls `notifyDeleteIndexQuery()` the query side to remove the index. Finally it removes the doctype for this instance in the `indexes` var.

The `notifyDeleteIndexQuery()` sends a http POST request to tell the query side to remove a particular index, using the `/fulltext/_delete_index_query/` route.

## Query

The query functions allows to query the indexes, using an index alias. The code is at [pkg/index/query.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/fulltext/search/query.go).

### Index Alias

In order to query on multiple indexes, we use an [IndexAlias](http://blevesearch.com/docs/IndexAlias/). When making a query, the list of doctypes to consider must be passed as an argument or else is set to default. Then each of the indexes is opened and added to the IndexAlias, allowing for a single interface to query on. After the query is done, all indexes are removed from the `IndexAlias` and closed.

### Query Index

To query the IndexAlias, we use a `QueryRequest` object that contains parameters on the query and flags on the desired return fields. Indeed, we can specify how many results we expect, with an optional `offset` and whether or not we want to have the `highlight`, the `name` and the `_rev` of the document as a result. Additionally, it is allowed to add a list of `sort` fields to order the results accordingly.

The `QueryIndex()` function first opens the `IndexAlias` with the corresponding doctypes. It then creates the `bleve.SearchRequest` object with the `BuildQuery()` function. It specifies the desired fields and params based on the `QueryRequest` object and creates Facets on the `created_at` and `docType` fields as an example.

Next `QueryIndex()` queries the `IndexAlias` using this `bleve.SearchRequest` to query all the underlying indexes.

The results are then formatted into a `SearchResult` object to be returned with the appropriate fields, using the `BuildResults()` function.

### Query Prefix Index

In order to make an auto-completion functionality, we allowed to query for a phrase prefix, using the `NewMatchPhrasePrefixQuery()` function implemented at [QwantResearch/bleve](https://github.com/QwantResearch/bleve/tree/Index) and based on this [pull request](https://github.com/blevesearch/bleve/pull/858).


## Endpoints

For interacting with the indexation and query side, we created different routes at [web/index/index.go](https://github.com/QwantResearch/cozy-stack/blob/Index/web/fulltext/fulltext.go).

### Query routes

* the `fulltext/_search` route gets a json object such as: 
    ```json
    { 
       "searchQuery": "cozy", 
       "instance": "cozy.tools:8080",
    }
    ```
     It calls `QueryIndex()` and returns a json object such as: 
    ```json 
    {
       "query":{
          "searchQuery":"qwant",
          "instance":"cozy.tools:8080"
       },
       "results":[
          {
             "_id":"...",
             "_type":"io.cozy.files",
             "name":"logo_cozy.pdf",
             "html_highlight":{
                "name":[
                   "logo_<mark>cozy.pdf</mark>"
                ]
             }
          }
       ]
    }
    ```
    The optionnal parameters are:
    * `numbResults` (int)
    * `highlight` (bool)
    * `name` (bool)
    * `_rev` (bool)
    * `offset` (int)
    * `sort` (list of strings)
    * `docTypes` (list of strings)
* the `fulltext/_search_prefix` route behaves the same as `fulltext/_search` but calls the `QueryPrefixIndex()` function instead.

### Indexation routes

* the `fulltext/_reindex` route directly calls `ReIndex()`. It requires a `docType` and `instance` string fields.
* the `fulltext/_reindex_all` route calls `ReIndexAll()`. It requires an `instance` string field.
* the `fulltext/_update_all_indexes` route directly calls `ReIndex()`. It requires an `instance` string field.
* the `fulltext/_update_index` route adds an update job calling `AddUpdateIndexJob()`. It requires a `docType` and `instance` string fields.
* the `fulltext/_update_index_alias/:instance/:doctype/:lang` route is made for the indexation side to send the updated index to the query side. It is called after an `IndexUpdate()`. It writes the body (that is the index) in a tmp file and renames it to the correct index name once it is entirely written (allowing for an atomic operation). 
* the `fulltext/_replicate_index` route calls `Replicate()`. It requires a `docType`, `lang` and `instance` string fields.
* the `fulltext/_replicate_all_indexes` route calls `ReplicateAll()`. It requires a `instance` string field.
* the `fulltext/_delete_index` route calls `DeleteIndexLock()`. It requires a `docType` and `instance` string fields and allows for an optional `querySide` bool field to delete the index on the query side as well.
* the `fulltext/_delete_all_indexes` route behaves the same as `fulltext/_delete_index` but calls the  `DeleteAllIndexesInstance()` function instead.
* the `fulltext/_delete_index_query/:instance/:doctype/:lang` route is made for the indexation side to tell the query side to delete the corresponding index. It is called after `fulltext/_delete_all_indexes` or `fulltext/_delete_index` if `querySide` is set to `true`.
* the `fulltext/_post_mapping/:doctype` route allows to send a new doctype mapping description file. The body is written in a tmp file and renamed to the correct description file name once it is entirely written (allowing for an atomic operation).
* the `fulltext/_fulltext_option` route allows to set indexing options for an instance. It requires an `instance` string field and allows the following boolean fields : `content` and `highlight` that respectively means to index or not the content, and to store or not the fields to allow for highlight information when querying. For `higlight` to take effect, it requires to call `fulltext/_reindex_all`. Indeed `highlight` implies modifying the mapping, that is done at index initialization. Similarly, for files indexed previously, `content` won't modify the index retroactively, it might be preferable to call `fulltext/_reindex_all` to obtain a coherent index. 

## Performances

Even if no test was made on the cozy server architecture, we did some local tests to get an order of magnitude of the indexation and query time, depending on the number of documents. 

For a bit more than 100 000 documents, using a batch size of 300 documents, we could index all the documents in 49,7 seconds, and we could query them it in 132ms.

The batch size of 300 was empirically chosen after having locally compared a batch size of 100, 500 and none. It needs to be tested on the cozy architecture to be conclusive.

Moreover, it appeared that the number of fields and the type of fields would change indexation time a lot (an order of magnitude of 2 times slower between a date and a text field). 


## TODOS

* Consider which solution is better for indexing a file content. Either separating content and metadata in two different indexes or storing the content value in the index and retrieving it if the content has not been modified.
* Test for performances on the cozy architecture,
* Test for a good batch size on the cozy architecture,
* Check that we are resilient in case of machine unavailability,
* Raise an error while querying or updating after a certain time.