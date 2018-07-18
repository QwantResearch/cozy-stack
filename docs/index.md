# Full-Text Search in Cozy Cloud
---

Using the [Bleve](http://blevesearch.com/) library, this git repository intends to contribute to the [cozy-stack](https://github.com/cozy/cozy-stack) by implementing full-text search functionalities.


## Goals

The goals are to efficiently suggest an architecture for full-text search, to prototype a solution and to test it, while taking into account the cozy-stack constraints :

* separation and autonomy between the indexation and the query sides,
* storage of the index on disk,
* one index per user and per [doctype](https://docs.cozy.io/en/cozy-doctypes/docs/README/) (and optionally per language),
* scalability constraints (in term of disk space, indexation time, and query time),
* periodic update of the index.

## Context

This implementation takes place in the context of the Qwant - CozyCloud partnership. It started in mid June 2018.

## Scope

In this module, we do not extract text from file, which means that another module must be implemented to return the file content. Except for files, all the information to index are contained in a [CouchDB](http://couchdb.apache.org/). 

To quickly get started, we focused on three doctypes : photo albums, files and bank accounts.

## Indexation

When an instance is started, indexes are either fetched and updated or created, filled and stored on the disk. Indeed, we have one index per doctype (3 implemented for now) and per language. For now, we only consider French and English language.

### Indexing

The indexing code is at [pkg/index/index.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/index/index.go). The indexes are stored on the default KVStore : [BoltDB](https://github.com/boltdb/bolt).

When an instance is started, we get the corresponding `bleve.Index` objects. It either fetch the one on the disk if it exists or else allocate a new one, set their mapping and store their couchdb sequence number to 0 (the default). 

Once all the indexes are fetched, they are all updated using the `AllIndexesUpdate()` function, that iterates through them and call `IndexUpdate()` on each. 

`IndexUpdate()` fetch the last couchdb sequence recorded in the index and use `couchdb.GetChanges()` to retrieve all the changes since that last sequence number. Since it is 0 when an index is created, it fetches all files in this couchdb.

Afterwards, there are three cases to take into account :

* the document is new. It means we have to detect its language and index it in the corresponding index.
* the document is updated and was already indexed. In this case, we need to find which index it belonged to, independently of its current language, to prevent having it indexed in two different indexes, and reindex it. Bleve doesn't allow to update specific fields, the entire document must be reindexed ([Bleve Google Groups](https://groups.google.com/forum/?utm_medium=email&utm_source=footer#!msg/bleve/v1E57t5lU3U/QTMLxaK9BwAJ)) : 

> There is no mechanism to update a single field value for a document.  You have to provide the entire document again, containing the updated field value.

* the document was indexed and is now trashed or destroyed. We have to find the index it belonged to and delete it from this index. *This last case hasn't been implemented yet.*

In term of implementation, since we don't know if the document is new or updated, we look for it in the indexes and if we couldn't find it, we consider it new.

The indexation is done by batch, which means that we have a batch for every language and we index them every 300 documents (in total, not per language). *The choice of 300 should be tested on the cozy architecture.*

Finally, the new sequence number returned by `couchdb.GetChanges()` is stored in every index. Thus, 2 indexes of the same doctype (French and English) will both have the same sequence number.

Finally, we add all the indexes to a `bleve.IndexAlias` object, so that we can query one unique index. *This task should be done by the query side of the module, so that both sides can be independent.*

### Mapping

Each index has its appropriate mapping (see [pkg/index/mapping.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/index/mapping.go)). 

We use 4 different types of field mapping : 

* a text field mapping, with 2 different analyzers : either the "fr" or the "en" analyzer, depending on the language. Bleve offers a [wizard](http://analysis.blevesearch.com/analysis) to see the behavior of the different analyzers, and to help create your own. For example, it might be relevant to create an analyzer for file names, so that "\_" is used as a token delimiter. A file such as "cozy\_cloud\_index.md" would then be returned on the query "index".
* a date type mapping, that accepts ISO 8601 format.
* a numeric field mapping, that accepts int, but convert numbers to float64 internally.
* a document disabled mapping to ignore subdocuments (such as "metadata", "referenced_by", etc. fields).

The correct document mapping is applied depending on the "docType" field in the document, but as indexes contain only one type of document, it could just be set to default mapping per index.

The usage of numeric fields should be selective ([Bleve Issue 831](https://github.com/blevesearch/bleve/issues/831)):

> In bleve today numeric fields are very expensive to index, as they are optimized for later doing numeric range searches. But, this optimization means that numeric fields can take up to 16x the space of text field with a single term. This is something we hope to improve in the future, but for now it means you have to be very selective about including numeric fields. Having lots of numeric fields means the index will be quite large (and consequently slow). 

### Language Detection

We use a [FastText](https://fasttext.cc) model ([lid.176.ftz](https://fasttext.cc/docs/en/language-identification.html)), to detect the language of a document.
Using a fasttext-go [wrapper](https://github.com/maudetes/fasttextgo), we can load the model once and then feed it with text to obtain the language prediction (see [pkg/index/language\_detection.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/index/language\_detection.go)).

For now, we predict the language based on the file name only, however, other fields might also be relevant for language identification. Moreover, in the case of the document being a file, the content should obviously be used to predict the language. 

Since we limit the languages to French and English, we iterate on the predicted languages to stop once we encounter one of these two. Indeed, if the file is actually Spanish, it will still classify it as the most probable language between French and English.

*There seems to be a bug in the language detection as it happens that not all languages are returned on a prediction, and especially neither "en" nor "fr". While awaiting an investigation, we return "en" by default.*

### Reindex

The `ReIndex()` function removes all the indexes from the disk and creates new ones.

It is safe to read from the indexes while reindexing, but not to write into them, due to the `IndexUpdate()` function it depends on.

*It means that we should block writes, or change the behavior to allow for writes concurrently.*

In order to do reindex, it starts by removing the files on the disk. Next it updates the existing index with the last changes (since reindexing might take a certain time and, while it processes, the old indexes will be used for querying) using `AllIndexesUpdate()`. Then it iterates through the indexes. For each it get an index at the same path as previously. Since the path was cleaned, it creates a new index. Afterwards it updates these indexes, using `IndexUpdate()`. 

It finally swaps the new indexes and the old ones in the index alias, closes the old index and updates the references to the new indexes.

### Replicate

The `ReplicateAll()` function calls `Replicate()` on each index.

It allows to read and write in the index in the meantime.

`Replicate()` obtains an `io.Writer` from the specified path. Next, it uses the `.Advanced()` function from bleve to access the store and get a reader. Then, it uses the `WriteTo()` function implemented in the Index branch of the repository [QwantResearch/bleve](https://github.com/QwantResearch/bleve/tree/Index).

This `WriteTo()` function was implemented for BoltDB only. It uses the `tx.WriteTo()` function from the store. From the BoltDB documentation : 
> You can use the Tx.WriteTo() function to write a consistent view of the database to a writer. If you call this from a read-only transaction, it will perform a hot backup and not block your other database reads and writes.

For now the `ReplicateAll()` functions copies the indexes to the same location, adding a ".save" extension.

Finally, it closes the file and the reader.

### Set and Get the sequence number

The functions `SetStoreSeq()` and `GetStoreSeq()` are used to store and retrieve the sequence number associated with an index, so that we can fetch the changes since this last sequence number.

In order to store the sequence number directly in the index, we use the bleve `SetInternal()` function that allows us to store additional information into the store, without it being taken into account in the index.

The `GetInteral()` function allows us to retrieve this information.

### Workers and Jobs

We added a new trigger to the `Triggers()` function that returns the list of triggers to add when an instance is created. This is a periodic trigger (`@every`) that triggers the indexupdate worker every 2min. 
Next, this worker simply calls the `AllIndexesUpdate()` function.

## Query

The query functions allows to query the indexes, using an index alias. The code is at [pkg/index/query.go](https://github.com/QwantResearch/cozy-stack/blob/Index/pkg/index/query.go).

*For now, the index alias is updated on the indexation side, which means that the two tasks can't be independent. Managing the index alias should be done on the query side and a route should be created to let the indexation side tell the query side when to update the index alias.*

### Query Index

To query the index, we use a `QueryRequest` object that contains parameters on the query and flags on the desired return fields. Indeed, we can specify how many results we expect and whether or not we want to have the `highlight`, the `name` and the `_rev` of the document as a result.

The `QueryIndex()` function creates the `bleve.SearchRequest`object. It specifies the desired field and the expected number of results (that depends on the `QueryRequest` object) and creates Facets on the `created_at` and `docType` fields as an example.

Next it searches the index alias using this `bleve.SearchRequest` to query all the underlying indexes.

The results are then formatted into a `SearchResult` object to be returned with the appropriate fields.

### Query Prefix Index

In order to make an auto-completion functionality, we allowed to query for a phrase prefix, using the `NewMatchPhrasePrefixQuery()` function implemented at [QwantResearch/bleve](https://github.com/QwantResearch/bleve/tree/Index) and based on this [pull request](https://github.com/blevesearch/bleve/pull/858).


## Endpoints

For interacting with the index, we created 3 different routes at [web/index/index.go](https://github.com/QwantResearch/cozy-stack/blob/Index/web/index/index.go).

* the `index/_search` route gets a json object such as `{ "searchQuery": "qwant"}`, calls `QueryIndex()` and returns a json object such as `{ "searchQuery": "qwant"}`. 
*For now, it doesn't take into account any parameter. The number of expected results is set to 15 and all the fields are set to true and will be returned.*
* the `index/_search_prefix` route behaves the same  but calls the `QueryPrefixIndex()` function instead.
* the `index/_reindex` route directly calls `ReIndex()`.

*Another route to call the replicate functions needs to be implemented.*

## Performances

Even if no test was made on the cozy server architecture, we did some local tests to get an order of magnitude of the indexation and query time, depending on the number of documents. 

For a bit more than 100 000 documents, using a batch size of 300 documents, we could index all the documents in 49,7 seconds, and we could query them it in 132ms.

The batch size of 300 was empirically chosen after having locally compared a batch size of é100, 500 and none. It needs to be tested on the cozy architecture to be conclusive.

Moreover, it appeared that the number of fields and the type of fields would change indexation time a lot (an order of magnitude of 2 times slower between a date and a text field). 


## TODOS

* Separate indexation and query by managing the index alias on the query side,
* Deal with multiple instance (when to create the indexes ? What path to chose ? etc.),
* Block writes (such as periodic jobs) on `ReIndex()` or change behavior to make sure to be consistent.
* In case of a file, use a `get_content()` function (not implemented yet) to index the content of this file,
* Consider separating the indexes for a file document and its content. Then, when a file is updated, check whether the content was or not updated as well (using the `md5sum` field for example) and update the indexes accordingly.
* Deal with files that are trashed or destroyed,
* Update the endpoint to take the different parameters into account,
* Create a `_replicate` or `_replicate_all` route, 
* Test for performances on the cozy architecture,
* Test for a good batch size on the cozy architecture,
* Deal with errors better.
